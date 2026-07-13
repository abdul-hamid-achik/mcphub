---
title: Gateway meta-tools
description: "Reference for the seven mcphub gateway meta-tools — list, search, describe, resolve, call, get_result, stats — and the lazy-mode discover-to-invoke flow."
---

# Gateway meta-tools

The mcphub gateway (`mcphub mcp serve`) registers **seven management tools** of
its own, alongside whatever downstream tools it mounts. They let an agent
inspect what is behind the gateway, discover a capability, invoke it, recover
an oversized result, and read local usage intelligence — all over the same
single stdio connection.

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
| [`mcphub_search_tools`](#mcphub-search-tools) | Substring search across the aggregated tool catalog. | You know roughly what capability you need. |
| [`mcphub_describe_tool`](#mcphub-describe-tool) | One tool's description and full JSON input schema. | You know the tool but not its arguments. |
| [`mcphub_resolve_tool`](#mcphub-resolve-tool) | Best-tool recommendation with required fields and an argument template. | You want search + describe in one round trip. |
| [`mcphub_call_tool`](#mcphub-call-tool) | Invoke a downstream tool by name through the gateway. | Always, in lazy mode — this is how everything runs. |
| [`mcphub_get_result`](#mcphub-get-result) | Page through an oversized result the gateway stored locally. | A call returned a `callId` receipt instead of the result. |
| [`mcphub_stats`](#mcphub-stats) | Local usage intelligence: calls, errors, estimated token cost. | You want to know what this session (or any) has been costing. |

## The lazy-mode flow

In lazy mode an agent works the catalog in a short loop —
**search → describe → call → get_result**:

```
mcphub_search_tools "semantic search"        # 1. discover: → vecgrep__vecgrep_search, ...
mcphub_describe_tool vecgrep__vecgrep_search # 2. inspect: description + input schema
mcphub_call_tool {server, tool, arguments}   # 3. invoke through the gateway
mcphub_get_result {callId, cursor}           # 4. only if the result was oversized
```

`mcphub_resolve_tool` collapses steps 1 and 2 into one call when you want a
direct recommendation instead of a candidate list. Steps 1–2 are also skippable
for [pinned](/guide/lazy-mode#pinning-keep-hot-tools-mounted) tools, which stay
mounted under their `server__tool` names even in lazy mode, and step 4 only
happens when a result exceeded the configured
[`response_budget`](/guide/results).

The gateway's MCP instructions tell the connecting model it is in lazy mode and
that the underlying tools *are* available, so a capable agent runs this loop
proactively without being prompted.

## mcphub_list_servers

Lists the configured downstream servers with their enabled/connected state and
tool counts.

**When to call it:** to orient — see what is actually behind the gateway before
searching for tools, or to check whether a server you expect is connected at
all. It is the tool-level counterpart of `mcphub list` on the CLI.

## mcphub_search_tools

Searches the aggregated tool catalog by substring across tool names and
descriptions, and returns the matching `server__tool` names (with server, tool,
and description) so you can call them via `mcphub_call_tool` without loading
every tool definition into context.

**When to call it:** whenever you need a capability and don't know (or don't
want to guess) which downstream tool provides it. In lazy mode this is the
front door to the entire catalog.

## mcphub_describe_tool

Returns a single downstream tool's description and its **full JSON input
schema** — enough to construct a valid `mcphub_call_tool` request. It accepts
the server and tool separately or the combined `server__tool` form.

**When to call it:** after a search hit (or when you already know the tool
name) but before invoking, if you are not certain what arguments the tool
expects. Skip it when the argument shape is obvious or already known.

## mcphub_resolve_tool

Finds the best tool for a task in **one** call: give it a natural-language
query (optionally capping the number of hits considered) and it returns a
single recommendation with its required fields and a ready-to-fill
**argument template**, plus a list of alternatives and an `ambiguous` flag when
several tools ranked equally. If nothing matches, it returns a hint to broaden
the query or fall back to `mcphub_search_tools`.

**When to call it:** when you want to go straight from intent to invocation —
it replaces the separate search + describe round trips. Prefer
`mcphub_search_tools` when you want to browse candidates yourself rather than
accept a ranking.

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
- [Configuration reference](/reference/config) — `expose`, `pin`,
  `response_budget`, `verbatim`, and per-agent `servers`/`tools` routing.
