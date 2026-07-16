---
title: Lazy mode
description: "Turn on expose: lazy so the mcphub gateway advertises only eight meta-tools, agents discover the rest on demand, and your most-used tools stay pinned."
---

# Lazy mode

By default (`expose: all`) the gateway mounts every downstream tool as
`server__tool`, and every agent session loads the full catalog — each tool's
name, description, and input schema — into context before a single call is
made. With a dozen servers behind the gateway, that's hundreds of tool
definitions paid for on every session, whether or not they get used.

`expose: lazy` flips the trade: the gateway advertises **only its eight
meta-tools** (plus anything you [pin](#pinning-keep-hot-tools-mounted)). The
downstream catalog stays fully available, but agents reach it on demand —
route their current task to a capability, inspect the one tool they need, and
invoke it through the gateway. A compact capability summary and per-server
`use_when` hints make unpinned tools discoverable without loading their full
schemas. The context cost of connecting N servers stays bounded and far below
mounting every tool.

## Turning it on

Set the top-level `expose` key in `mcphub.yaml`:

```yaml
version: 1

# all  (default) — mount every downstream tool as 'server__tool'
# lazy           — advertise only mcphub's meta-tools; agents resolve or search
#                  capabilities and invoke through mcphub_call_tool
expose: lazy
```

You can also flip exposure with **x** in the [Studio TUI](/guide/studio).

The setting is read when the gateway starts, so restart your agents (each one
runs its own `mcphub mcp serve`) to pick it up. No `mcphub sync` is needed —
the agent's harness entry is unchanged; only what the gateway advertises
changes.

::: tip Lazy mode is a gateway feature
`expose` only affects agents in `gateway` mode. An agent in `direct` mode
talks to each server itself, so it always loads every enabled server's full
tool list. See [Concepts](/guide/concepts) for the two modes.
:::

## The eight meta-tools

In lazy mode this is the entire advertised surface:

| Tool | What it does |
| --- | --- |
| `mcphub_list_servers` | List configured downstream servers with enabled/connected state and tool counts. |
| `mcphub_search_tools` | Ranked natural-language search across tool and server metadata; returns up to 20 matching `server__tool` names by default. |
| `mcphub_describe_tool` | Return one tool's description and full JSON input schema. |
| `mcphub_resolve_tool` | Route the current goal or activity to the best tool in one call, with match evidence, required fields, an argument template, alternatives, and an ambiguity flag. |
| `mcphub_call_tool` | Invoke a downstream tool by name — how everything gets called in lazy mode. `detach: true` runs a long-running tool in the background and returns a `callId` at once. |
| `mcphub_get_result` | Page through an oversized result the gateway stored locally (see below). |
| `mcphub_poll_result` | Check a detached call by `callId` and collect its result when done. |
| `mcphub_stats` | Local usage intelligence: calls, errors, estimated token cost, per-server breakdown. |

The gateway's MCP instructions tell the connecting model it is in lazy mode,
include a compact summary of its in-scope capability families, and ask it to
resolve context at the start of a non-trivial task and whenever work changes
phase (research → planning → implementation → verification). Harnesses that
pass MCP server instructions to their model can therefore discover and call
unpinned tools proactively.

Give each server one or more concise routing hints when its name or tool names
do not communicate the user outcome:

```yaml
servers:
  hitspec:
    command: hitspec
    args: [mcp, serve, --workspace, /absolute/api-workspace]
    enabled: true
    description: Bounded HTTP fetches and saved-request validation
    tags: [http, api, markdown]
    use_when:
      - fetch a public HTTP URL as raw, text, Markdown, or JSON
      - list or validate saved .http and .hitspec requests
```

These hints do not mount or pin anything. They are lightweight vocabulary for
the resolver and search index.

## The discovery loop

A lazy-mode agent works the catalog in three steps.

**1. Route or browse.** Prefer the contextual resolver when the agent knows
what it is doing but not which server owns the capability:

```json
// mcphub_resolve_tool
{ "query": "fetch this public URL as Markdown for research" }
```

It tokenizes the full sentence and ranks the connected, in-scope catalog
across tool names, titles, descriptions, bounded top-level input field names,
plus server names, descriptions, tags, and `use_when` hints. For browsing, use
the same natural-language query with `mcphub_search_tools`:

```json
// mcphub_search_tools
{ "query": "semantic search across this repository", "max_hits": 10 }
```

The response reports total `count`, bounded `returned`, `truncated`, and ranked
matches with their `namespaced` (`server__tool`) name, server/tool metadata,
score, and matched terms. `max_hits` defaults to 20 and is capped at 100. A
2,048-byte query cap and 12 KiB compact match-array budget prevent discovery
itself from becoming a context spike; `byte_limited` and
`metadata_truncated` report those bounds.

**2. Inspect.** Two options, depending on how much the agent already knows:

- `mcphub_describe_tool` takes `{server, tool}` — or just `tool` in the
  combined `server__tool` form — and returns the tool's description and full
  JSON `input_schema`, enough to construct a valid call.
- `mcphub_resolve_tool` already collapses route + describe into one round trip:
  give it a natural-language `query` (and optionally `max_hits`, default 5) and it
  returns one recommendation with `required_fields` and a ready-to-fill
  `argument_template`, a list of alternatives, and an `ambiguous` flag when
  several tools ranked equally. If `argument_template_truncated` is true, use
  `mcphub_describe_tool` for the complete schema before the call.

**3. Invoke.** Call through the gateway:

```json
// mcphub_call_tool
{ "server": "vecgrep", "tool": "vecgrep_search", "arguments": { "query": "auth middleware" } }
```

`tool` may also be the combined form (`"tool": "vecgrep__vecgrep_search"`),
with or without `server` set — the gateway routes it either way. Every call is
recorded to the [intelligence store](/guide/intelligence), same as in
`expose: all`.

### Oversized results

If a downstream result exceeds the response budget, `mcphub_call_tool` returns
a compact receipt with a `callId` instead of flooding the context. The exact
serialized result is stored locally for 24 hours, and the agent recovers it in
bounded base64 pages with `mcphub_get_result`: start at `cursor: 0` and follow
`nextCursor` until `done` is `true`. Small results pass through unchanged.

## Pinning: keep hot tools mounted

Discovery costs a round trip or two. For the tools you call constantly, skip
it: pins stay mounted directly on the gateway even under `expose: lazy`, so
agents call them by their `server__tool` name automatically.

```sh
mcphub pin codemap vecgrep              # whole servers (all their tools)
mcphub pin codemap__*                   # same, explicit wildcard
mcphub pin codemap__codemap_semantic    # one tool
mcphub pin --top 8                      # auto-pin your 8 most-called tools (from stats)
mcphub pin                              # list current pins
```

`pin --top N` reads the local intelligence store and pins your N most-called
tools — run `mcphub stats --tools` first to see what it would choose. Pins
land in `mcphub.yaml` under a top-level `pin:` list, so they survive in
version control like the rest of your config; saving validates them, so a pin
naming an unknown server is rejected.

Removing pins mirrors adding them:

```sh
mcphub unpin codemap__codemap_semantic  # remove one exact pin
mcphub unpin codemap                    # remove every pin resolving to that server
```

::: tip Pins apply on the next gateway start
In gateway mode no sync is needed — restart your agents so their gateway
processes reload the config.
:::

::: warning Scoped agents
If an agent has [per-agent routing](/guide/routing) (`servers:` /
`tools:` lists), a pin outside that agent's scope is silently skipped for it.
The pin still applies to unscoped agents.
:::

## When to prefer `expose: all`

Lazy mode is a trade, not a strict upgrade:

- **Small catalogs.** With one or two servers exposing a handful of tools,
  the eight meta-tools plus the indirection can cost as much as just mounting
  everything.
- **Extra round trips.** Each first use of a tool costs a search/resolve call
  before the real one. Pinning removes this for hot paths, but a workload that
  touches many different tools once each pays it repeatedly.
- **Weaker instruction-following.** Lazy mode relies on the model reading the
  gateway's instructions and discovering tools proactively. Agents that only
  use tools listed up front will underuse the catalog; give those
  `expose: all` (or pin generously).

A good middle ground: `expose: lazy` plus `mcphub pin --top N`, revisited
occasionally as [`mcphub stats`](/guide/intelligence) shows your usage
shifting.

## See also

- [Contextual routing for harnesses](/guide/contextual-routing) — when a
  harness should resolve, how a host-assisted advisor behaves, and the safety
  contract around recommendations.
- [Gateway meta-tools](/reference/meta-tools) — full reference for all eight
  meta-tools, including their exact call shapes.
- [Bounded, lossless results](/guide/results) — the `callId` receipt and
  `mcphub_get_result` paging contract behind oversized calls.
- [Per-agent routing](/guide/routing) — scoping servers and tools per agent,
  which also filters what a lazy-mode agent can discover and pin.
- [Configuration reference](/reference/config) — `expose`, `pin`, and per-server `use_when` hints.
