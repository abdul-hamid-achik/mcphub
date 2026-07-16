---
title: Gateway meta-tools
description: "Reference for the eight mcphub gateway meta-tools — list, search, describe, resolve, call, get_result, poll_result, stats — and the lazy-mode discover-to-invoke flow."
---

# Gateway meta-tools

The mcphub gateway (`mcphub mcp serve`) registers **eight management tools** of
its own, alongside whatever downstream tools it mounts. They let an agent
inspect what is behind the gateway, discover a capability, invoke it (in the
foreground or detached into the background), recover an oversized result, and
read local usage intelligence — all over the same single stdio connection.

The meta-tools are registered in **both** exposure modes:

- **`expose: all`** (default) — every downstream tool is also mounted directly
  as `server__tool`, so agents mostly call tools by name and use the meta-tools
  for introspection.
- **`expose: lazy`** — the meta-tools (plus any [pins](/guide/lazy-mode#pinning-keep-hot-tools-mounted))
  are the *entire* advertised surface. Agents discover downstream tools on
  demand and invoke everything through `mcphub_call_tool`. See
  [Lazy mode](/guide/lazy-mode) for the trade-offs.

## At a glance

| Tool | What it does | Call it when |
| --- | --- | --- |
| [`mcphub_list_servers`](#mcphub-list-servers) | List downstream servers with enabled/connected state and tool counts. | You want to know what is behind the gateway. |
| [`mcphub_search_tools`](#mcphub-search-tools) | Ranked natural-language search across tool + server metadata. | You want to browse several capability candidates. |
| [`mcphub_describe_tool`](#mcphub-describe-tool) | One tool's description and full JSON input schema. | You know the tool but not its arguments. |
| [`mcphub_resolve_tool`](#mcphub-resolve-tool) | Context router with match evidence, required fields, and an argument template. | A task starts or changes phase and you want the best hidden tool. |
| [`mcphub_call_tool`](#mcphub-call-tool) | Invoke a downstream tool by name through the gateway. | Always, in lazy mode — this is how everything runs. |
| [`mcphub_get_result`](#mcphub-get-result) | Page through an oversized result the gateway stored locally. | A call returned a `callId` receipt instead of the result. |
| [`mcphub_poll_result`](#mcphub-poll-result) | Check a detached (`detach: true`) call and collect its result. | You started a long-running call in the background. |
| [`mcphub_stats`](#mcphub-stats) | Local usage intelligence: calls, errors, estimated token cost. | You want to know what this session (or any) has been costing. |

## The lazy-mode flow

In lazy mode an agent works the catalog in a short loop —
**resolve/search → describe if needed → call → get_result**:

```
mcphub_resolve_tool "find code by meaning"   # 1. route: → vecgrep__vecgrep_search + template
mcphub_describe_tool vecgrep__vecgrep_search # 2. optional: inspect the complete schema
mcphub_call_tool {server, tool, arguments}   # 3. invoke through the gateway
mcphub_get_result {callId, cursor}           # 4. only if the result was oversized
```

`mcphub_resolve_tool` collapses steps 1 and 2 into one call when you want a
direct recommendation instead of a candidate list. Steps 1–2 are also skippable
for [pinned](/guide/lazy-mode#pinning-keep-hot-tools-mounted) tools, which stay
mounted under their `server__tool` names even in lazy mode, and step 4 only
happens when a result exceeded the configured
[`response_budget`](/guide/results).

The gateway's MCP instructions tell the connecting model it is in lazy mode,
that the underlying tools *are* available, and that it should resolve context
at task start and phase changes. They also carry a bounded, scope-aware
capability summary that prefers each server's `use_when`, then falls back to
its description or tags. An exact tool allowlist lists only those tool names,
so a capable harness can run this loop proactively without leaking broader
out-of-scope capabilities.

## mcphub_list_servers

Lists the configured downstream servers with their enabled/connected state and
tool counts.

**When to call it:** to orient — see what is actually behind the gateway before
searching for tools, or to check whether a server you expect is connected at
all. It is the tool-level counterpart of `mcphub list` on the CLI.

## mcphub_search_tools

Tokenizes a natural-language query and ranks the aggregated catalog across tool
names, titles, descriptions, bounded top-level input field names, and server
names, descriptions, tags, and `use_when` hints.
It returns the matching `server__tool` names with routing evidence so you can
call them through `mcphub_call_tool` without loading every definition. The
optional `max_hits` defaults to 20 and is capped at 100; `count`, `returned`,
and `truncated` make the count bound explicit. Queries are capped at 2,048
bytes, and compact match metadata is also capped by a 12 KiB match-array
budget; `byte_limited` and per-match `metadata_truncated` disclose those cuts.

**When to call it:** whenever you need a capability and don't know (or don't
want to guess) which downstream tool provides it. In lazy mode this is the
front door to the entire catalog.

## mcphub_describe_tool

Returns a single downstream tool's description and its **full JSON input
schema** — enough to construct a valid `mcphub_call_tool` request. It accepts
the server and tool separately or the combined `server__tool` form.

Many downstream servers self-prefix their tool names (hitspec's search tool is
`hitspec_search_web`), which makes the namespaced form stutter
(`hitspec__hitspec_search_web`). Since v0.16.1 the gateway also resolves the
**stutter-collapsed alias**: `hitspec__search_web` — or
`{server: "hitspec", tool: "search_web"}` — resolves to the canonical
downstream name whenever the bare name matches nothing on that server. The
exact name always wins, so a real downstream tool can never be shadowed by the
alias. This applies to `mcphub_describe_tool` and to `mcphub_call_tool` (both
synchronous and detached); responses, receipts, and telemetry always report
the canonical name.

**When to call it:** after a search hit (or when you already know the tool
name) but before invoking, if you are not certain what arguments the tool
expects. Skip it when the argument shape is obvious or already known.

## mcphub_resolve_tool

Finds the best tool for the current goal or activity in **one** call. The
resolver uses the same intent-aware catalog ranking as search, then returns a
single recommendation with `score`, `matched_terms`, server description,
`use_when`, required fields, and a ready-to-fill **argument template**. It also
returns ranked alternatives plus an explicit `status` (`confident`, `ambiguous`,
or `no_match`). Weak term coverage and close scores remain ambiguous instead of
turning any positive lexical hit into a clear route. `reason_codes`,
`matched_fraction`, and `score_gap` explain the bounded decision. If nothing
matches, it suggests different vocabulary, adding a `use_when` hint, or browsing
with `mcphub_search_tools`.

Every response includes `contract_version` and a content-addressed
`catalog_revision` for the connected, in-scope catalog. Harness caches should
invalidate on revision changes and apply a short TTL to `no_match`.

The resolver uses the same 2,048-byte query and compact-metadata bounds.
Argument templates are capped at 48 fields / 2,048 field-name bytes; when
`argument_template_truncated` is true, call `mcphub_describe_tool` for the full
schema before invoking the recommendation. `alternatives_truncated` similarly
signals a count or byte-bound alternative list.

**When to call it:** proactively when a non-trivial task starts or changes
phase, and whenever you want to go straight from intent to invocation. It
replaces the separate search + describe round trips. Prefer
`mcphub_search_tools` when you want to browse candidates yourself rather than
accept a ranking. Harness authors can use the
[contextual routing integration contract](/guide/contextual-routing) to add
model-driven or host-assisted discovery without hardcoding server mappings.

## mcphub_call_tool

Invokes a downstream tool by name through the gateway. It accepts
`{server, tool, arguments}`, where `tool` may also be the combined
`server__tool` form — with or without `server` set, the gateway routes it
either way (a redundant `server__` prefix is stripped when both are given, so
echoing the `namespaced` field from a search result works).

Small results pass through unchanged. Oversized results return a lossless
retrieval receipt for `mcphub_get_result` instead (see below). Every call is
timed and recorded to the local [intelligence store](/guide/intelligence),
exactly like directly mounted `server__tool` calls.

Transport failures are reported as **outcome unknown**. mcphub reconnects the
server for future calls but deliberately does not replay the request: receiving
no response does not prove that a downstream mutation did not happen.

Two optional arguments cover long-running downstream work:

- **`detach: true`** — start the call in the background and return an
  `accepted` receipt with a `callId` immediately, instead of holding the
  request open. Use it for tools that can outlive the *client's* tool-call
  timeout — repository indexing, large scans, batch jobs. Collect the outcome
  with [`mcphub_poll_result`](#mcphub-poll-result). At most 8 detached calls
  run at once.
- **`timeout_ms`** — bound the call from the gateway side, clamped by the
  [`call_timeout`](/reference/config) config (default 30m). On a synchronous
  call it can only shorten the effective deadline (the client's own deadline
  still applies); on a detached call it bounds the background execution.

**When to call it:** in lazy mode, always — this is how every non-pinned
downstream tool runs. In `expose: all` it still works, but agents normally call
mounted tools by name instead.

::: warning Scoped agents
If the gateway was launched with [per-agent routing](/guide/routing)
(`mcphub mcp serve --agent <name>` with `servers:`/`tools:` lists in
`mcphub.yaml`), `mcphub_call_tool` refuses out-of-scope calls. Discovery and
retrieval respect the same scope.
:::

## mcphub_get_result

Retrieves a bounded, base64-encoded page of a complete result the gateway
previously stored. Pass the `callId` from a retrieval receipt and a byte
`cursor`: start at `0`, then keep following each response's `nextCursor` until
`done` is `true`. Paging reads a byte range straight out of SQLite; a page is
sized to fit your `response_budget` (capped at 8KB).

**When to call it:** only after `mcphub_call_tool` (or a mounted tool call)
came back with a `"status": "stored"` receipt — and only if you actually need
the rest of the payload. The receipt's preview is often enough.

### The `callId` receipt, conceptually

When a downstream result exceeds the configured `response_budget` (default
32KB), the gateway does **not** truncate it. The exact serialized result is
spooled to the local SQLite store for **24 hours** under an opaque `callId`,
and the agent receives a compact receipt in its place. Conceptually the receipt
carries:

- `status: "stored"` and the **`callId`** — the retrieval handle,
- which server/tool produced it and the original size versus the budget,
- a best-effort text `preview`, when it fits inside the budget,
- a `nextAction` hint telling the agent to page with `mcphub_get_result` from
  `cursor: 0` until `done` is `true`.

An unknown or expired `callId` comes back as `status: "unavailable"` (not an
error), and retrieval re-checks the stored server/tool against the calling
agent's scope before returning any bytes. Full receipt and page shapes, opt-outs
(`verbatim: true`, `response_budget: "0"`), and budget tuning are covered in
[Bounded, lossless results](/guide/results).

## mcphub_poll_result

Checks on a detached call started with `mcphub_call_tool {detach: true}` and,
once the downstream call has finished, hands back its result. Pass the
`callId` from the `accepted` receipt:

- while the downstream call is still running it returns `status: "pending"`
  with an `elapsedMs` — poll again after a delay;
- if the call failed it returns `status: "failed"` with the error text;
- once complete it returns the **tool result itself**, exactly as a
  synchronous call would have — an oversized result appears as a stored-result
  receipt to page with `mcphub_get_result`. Re-polling a completed call is
  idempotent for its retention window.

Completed detached results are retained in memory for 24 hours (matching the
result spool's retention) with a bounded registry; the registry does **not**
survive a gateway restart. A `callId` that is expired, evicted, never issued,
or from before a restart reports `status: "unknown"`, and the call must be
re-run.

**When to call it:** only after a detached `mcphub_call_tool` returned an
`accepted` receipt. For `callId`s that came from a `"status": "stored"`
receipt, use `mcphub_get_result` instead.

## mcphub_stats

Returns the gateway's local usage intelligence: total calls, error count,
estimated token cost, and a per-server breakdown — the same data
[`mcphub stats`](/reference/cli) reads from the SQLite store.

**When to call it:** when an agent (or you, through one) wants to reason about
which servers are earning their context budget — for example before suggesting
what to pin or disable. For human consumption, the CLI's `mcphub stats`
(`--tools`, `--recent N`, `--since 7d`, `--markdown`, `--json`) is richer; see
[Intelligence](/guide/intelligence).

::: tip Meta-tools are recorded too
Every proxied downstream call lands in the intelligence store regardless of
how it was invoked — mounted `server__tool` name or `mcphub_call_tool` — so
`mcphub pin --top N` and `mcphub stats` see your real usage either way.
:::

## See also

- [Lazy mode](/guide/lazy-mode) — turning on `expose: lazy`, pinning, and when
  to prefer `expose: all`.
- [Bounded, lossless results](/guide/results) — the full receipt and paging
  contract behind `mcphub_get_result`.
- [Intelligence](/guide/intelligence) — what the store records and how
  `mcphub stats` reports it.
- [Configuration reference](/reference/config) — `expose`, `pin`, `use_when`,
  `response_budget`, `verbatim`, and per-agent `servers`/`tools` routing.
