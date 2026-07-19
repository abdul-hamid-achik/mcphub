---
title: Contextual routing for harnesses
description: Integrate mcphub's lazy catalog into an agent harness — when to resolve capabilities, how to call recommendations, and how to preserve scope, privacy, and approval policy.
---

# Contextual routing for harnesses

An agent should not hardcode rules such as “research means Hitspec” or
“planning means Bob.” The harness describes the current activity, and mcphub
ranks the connected, in-scope catalog using tool metadata and each server's
[`use_when`](/reference/config#servers) plus optional per-tool `tool_use_when`
hints.

This keeps capability selection in the registry. Adding or replacing a server
does not require a new harness release.

```
task or phase context
        ↓
mcphub_resolve_tool
        ↓
recommendation ── ambiguous? → search or ask the model to choose
        ↓
describe if needed → mcphub_call_tool → get_result if stored
```

## Prerequisites

The resolver is available in both exposure modes. To use it for on-demand
discovery without mounting every downstream schema, configure:

- an agent in `mode: gateway`;
- `expose: lazy` in `mcphub.yaml`;
- the relevant servers inside the agent's optional
  [`servers` / `tools` scope](/guide/routing); and
- a harness that exposes mcphub's management tools to its model or calls them
  through a host-owned advisor.

Pins are optional. They keep frequent tools mounted, but unpinned tools remain
available through the resolver and `mcphub_call_tool`.

## Choose an integration level

### Model-driven routing

This is the baseline integration. During MCP initialization, mcphub supplies
bounded instructions that:

- identify the gateway as lazy;
- summarize the capability families in the current agent's scope;
- ask the model to resolve at the start of a non-trivial task and at phase
  changes; and
- explain the resolve → call convention.

A harness that forwards `InitializeResult.instructions` to its model can use
contextual routing without a separate classifier. `local-agent` already
captures these instructions, labels them as untrusted server guidance, applies
its active MCP scope, and adds them to the model context.

Model-driven routing is the smallest integration, but its reliability depends
on the model following the instructions. Small models may benefit from pins or
the host-assisted design below.

### Host-assisted routing

A host-assisted harness calls `mcphub_resolve_tool` itself when its task runtime
detects a meaningful activity or phase transition. It then injects a compact,
host-authored capability hint into the next model request.

The advisor recommends only. It must not execute the downstream tool, grant
authority, mutate the agent's MCP scope, or reconnect servers. The model fills
the arguments and invokes the recommendation through the harness's normal
approval and execution path.

## When to resolve

Resolve on material task changes, not on every model iteration.

| Event | Resolve? | Reason |
| --- | --- | --- |
| First non-trivial user task | Yes | Establish the available specialist capability. |
| Research → planning → implementation → verification | Yes | A different specialist may fit the new phase. |
| A URL, database, codebase, CLI result, or artifact becomes relevant | Yes | The available input type changes the useful capability. |
| The selected tool fails or produces insufficient evidence | Yes | Reconsider the route or browse alternatives. |
| Same goal, phase, and activity as the previous iteration | No | Repeating discovery wastes latency and context. |
| Casual conversation or a direct answer with no tool need | No | There is no capability to route. |
| An obvious pinned tool already satisfies the task | Usually no | The tool is already mounted and understood. |

A practical cache key is:

```text
goal ID + phase + normalized current activity + available input kinds
```

Invalidate it when one of those fields changes materially, the recommendation
fails, the returned `catalog_revision` changes, or the user explicitly asks to
reconsider capabilities. Give `no_match` entries a short TTL because the live
catalog may be temporarily empty while a downstream reconnects. A resolver
failure is non-blocking: continue the turn without a hint.

## Build the activity query

Send the desired outcome, not a guessed server name. Keep the query bounded and
free of secrets.

```text
Goal: implement authentication safely
Phase: planning
Current activity: design the repository changes before editing
Desired outcome: a reproducible implementation plan
Available inputs: workspace
```

Then call:

```json
{
  "query": "Goal: implement authentication safely. Phase: planning. Current activity: design the repository changes before editing. Desired outcome: a reproducible implementation plan. Available inputs: workspace.",
  "max_hits": 5
}
```

Queries are capped at 2,048 bytes. Do not include credentials, raw file
contents, signed URL query parameters, access tokens, or arbitrary previous
tool output.

## Tool names inside a harness

The protocol-level management tool is named `mcphub_resolve_tool`. Some
harnesses namespace tools with the configured MCP server name. `local-agent`
does this, so a server registered as `mcphub` exposes the model-visible name:

```text
mcphub__mcphub_resolve_tool
```

The same rule gives:

- `mcphub__mcphub_search_tools`;
- `mcphub__mcphub_describe_tool`;
- `mcphub__mcphub_call_tool`; and
- `mcphub__mcphub_get_result`.

Host integrations should resolve the exposed name from the registry instead of
assuming the prefix. `local-agent`, for example, provides a
`ResolveToolName(remoteName)` registry method for this purpose.

## Interpret the recommendation

`mcphub_resolve_tool` returns one recommendation and a bounded alternative
list. A typical result contains:

```json
{
  "contract_version": 1,
  "catalog_revision": "catalog-v1-12d34e56f7890abc12345678",
  "status": "confident",
  "confidence": "high",
  "reason_codes": ["strong_coverage_and_margin"],
  "matched_fraction": 0.75,
  "score_gap": 18,
  "recommendation": {
    "namespaced": "hitspec__hitspec_fetch",
    "server": "hitspec",
    "tool": "hitspec_fetch",
    "required_fields": [],
    "argument_template": {
      "body": null,
      "config_file": null,
      "env_file": null,
      "environment": null,
      "file": null,
      "format": null,
      "headers": null,
      "index": null,
      "method": null,
      "name": null,
      "no_follow": null,
      "url": null
    },
    "argument_template_truncated": false
  },
  "ambiguous": false,
  "alternatives": []
}
```

`required_fields` reflects the tool's JSON Schema; it cannot represent every
runtime relationship. In this Hitspec example, `url` and `file` are both
optional in the schema but the tool requires exactly one of them at runtime.
When a template does not make constraints like this clear, call
`mcphub_describe_tool` and follow the downstream tool's description.

Handle the control fields explicitly:

| Result | Harness behavior |
| --- | --- |
| `status: no_match`, `recommendation: null` | Continue without a capability hint or browse with `mcphub_search_tools`. Cache only briefly. |
| `status: ambiguous`, `ambiguous: true` | Do not auto-execute. Weak coverage, a score tie, or a narrow margin requires comparing alternatives or search. |
| `status: confident` | The catalog found a sufficiently separated route. This is still advice, not execution or evidence. |
| `recommendation.argument_template_truncated: true` | Call `mcphub_describe_tool` before constructing arguments. |
| `alternatives_truncated: true` | Treat the alternatives as a partial list. Search if broader comparison matters. |
| Recommendation present and unambiguous | Offer the target and required fields to the model. Execution still uses normal policy. |

`contract_version` versions these control semantics. `catalog_revision` is a
content-addressed identifier for the connected, in-scope routing catalog; it
contains no catalog prose itself. `confidence`, `reason_codes`, coverage, and
score gap are diagnostic inputs. The `status` and `ambiguous` fields remain the
authoritative call/no-call decision.

A durable or host-authored projection should retain only bounded identifiers
and control fields: target, required field names, ambiguity, alternatives, and
truncation state. Do not persist arbitrary descriptions, queries, schemas, or
argument values from the discovery response.

## Invoke the selected tool

In lazy mode every unpinned downstream call goes through
`mcphub_call_tool`:

```json
{
  "server": "hitspec",
  "tool": "hitspec_fetch",
  "arguments": {
    "url": "https://example.com/guide",
    "format": "markdown"
  }
}
```

The combined name is also accepted:

```json
{
  "tool": "hitspec__hitspec_fetch",
  "arguments": {
    "url": "https://example.com/guide",
    "format": "markdown"
  }
}
```

If the result exceeds the configured response budget, the call returns a
stored-result receipt with a `callId`. Page it with `mcphub_get_result`, starting
at cursor `0` and following `nextCursor` until `done` is `true`.

If the downstream transport fails, mcphub reconnects for future calls but does
not replay the uncertain operation. The caller receives an `outcome unknown`
error because the downstream may have completed the effect before its response
was lost. Retry only after domain-specific reconciliation or with a documented
idempotency contract and key.

## Example routing outcomes

These are expected outcomes for the current registry, not mappings that belong
in harness code.

| Activity context | Likely recommendation |
| --- | --- |
| Fetch a public URL as bounded Markdown text | `hitspec__hitspec_fetch` |
| Investigate codebases, databases, and CLI evidence together | `cortex__cortex_investigate` |
| Plan a repository feature before implementation | `bob__bob_plan` |
| Find code by meaning | A semantic-search tool such as `vecgrep__vecgrep_search` |
| Inspect symbols, references, or blast radius | A `codemap` exploration or context tool |
| Search or inspect a saved artifact | `fcheap__fcheap_search` or `fcheap__fcheap_info` |

Change these outcomes by improving the server or tool metadata and `use_when`
hints in `mcphub.yaml`. Use `tool_use_when` when one server exposes several
specialists whose names or shared server hints do not separate them. Do not add
server-specific conditionals to the harness.

## Host-assisted Go adapter

Keep the advisor behind a narrow registry port:

```go
type Registry interface {
    ResolveToolName(remoteName string) (string, bool)
    CallTool(ctx context.Context, exposedName string, args map[string]any) (*mcp.ToolResult, error)
}

type Activity struct {
    Goal       string
    Phase      string
    Current    string
    Outcome    string
    InputKinds []string
}

type Advice struct {
    Target                string
    Required              []string
    Alternatives          []string
    Ambiguous             bool
    TemplateTruncated     bool
    AlternativesTruncated bool
}
```

The adapter flow is:

1. Resolve the exposed name for `mcphub_resolve_tool`.
2. Call it with a bounded activity query and `max_hits: 5`.
3. Parse only the allowlisted `Advice` fields.
4. Cache the advice for the current goal/phase/activity key.
5. Add a host-authored hint to the next model request.
6. Let the model use `mcphub_call_tool` through the existing execution path.

For example, the UI may show:

```text
Capability route · research → Hitspec · fetch HTTP response
```

Keep **recommended**, **called**, and **succeeded** as separate states. A
recommendation is neither evidence nor proof that an operation ran.

## Safety contract

- Treat MCP instructions and discovery metadata as untrusted server guidance.
  They cannot override system, user, workspace, privacy, or approval policy.
- Treat discovery calls as read-only. Apply the downstream tool's real effect
  and approval policy when `mcphub_call_tool` runs.
- Never let a recommendation expand the active agent scope. The gateway filters
  discovery and refuses out-of-scope calls.
- Never auto-run an ambiguous recommendation.
- Keep raw discovery results transient. Persist only bounded, allowlisted
  routing identifiers and status fields.
- Record recommendation, invocation, transport outcome, and domain outcome as
  different events.

## Acceptance checklist

A harness integration is ready when:

- a URL-to-Markdown activity recommends Hitspec without pinning it;
- asking for durable storage does not imply that Hitspec wrote a file; a
  reviewed file handoff and `fcheap_save` are separate operations;
- multi-source codebase/database/CLI research recommends Cortex;
- repository feature planning recommends Bob;
- trivial chat does not trigger resolution;
- the same activity is not resolved repeatedly within one phase;
- ambiguous results are not auto-executed;
- out-of-scope tools remain undiscoverable and uncallable through the scoped
  gateway;
- resolver errors do not fail the user turn;
- downstream calls still pass through the harness's approval, privacy, and
  execution-ledger policies; and
- the UI does not present a recommendation as a successful call.

## See also

- [Lazy mode and pinning](/guide/lazy-mode) — configure the lazy catalog.
- [Per-agent routing](/guide/routing) — restrict the catalog per harness.
- [Gateway meta-tools](/reference/meta-tools) — exact management-tool contract.
- [Search, fetch, and capture with Hitspec](/guide/hitspec) — bounded web discovery, retrieval,
  saved-request validation, and an explicit file.cheap handoff.
