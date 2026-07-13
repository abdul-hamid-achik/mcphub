---
title: Lazy mode
description: "Turn on expose: lazy so the mcphub gateway advertises only seven meta-tools, agents discover the rest on demand, and your most-used tools stay pinned."
---

# Lazy mode

By default (`expose: all`) the gateway mounts every downstream tool as
`server__tool`, and every agent session loads the full catalog — each tool's
name, description, and input schema — into context before a single call is
made. With a dozen servers behind the gateway, that's hundreds of tool
definitions paid for on every session, whether or not they get used.

`expose: lazy` flips the trade: the gateway advertises **only its seven
meta-tools** (plus anything you [pin](#pinning-keep-hot-tools-mounted)). The
downstream catalog stays fully available, but agents reach it on demand —
search for a capability, inspect the one tool they need, and invoke it through
the gateway. The context cost of connecting N servers drops to a small
constant.

## Turning it on

Set the top-level `expose` key in `mcphub.yaml`:

```yaml
version: 1

# all  (default) — mount every downstream tool as 'server__tool'
# lazy           — advertise only mcphub's meta-tools; agents discover via
#                  mcphub_search_tools and invoke through mcphub_call_tool
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

## The seven meta-tools

In lazy mode this is the entire advertised surface:

| Tool | What it does |
| --- | --- |
| `mcphub_list_servers` | List configured downstream servers with enabled/connected state and tool counts. |
| `mcphub_search_tools` | Substring search across tool names and descriptions; returns matching `server__tool` names. |
| `mcphub_describe_tool` | Return one tool's description and full JSON input schema. |
| `mcphub_resolve_tool` | Find the best tool for a task in one call: a recommendation with required fields and an argument template, plus alternatives and an ambiguity flag. |
| `mcphub_call_tool` | Invoke a downstream tool by name — how everything gets called in lazy mode. |
| `mcphub_get_result` | Page through an oversized result the gateway stored locally (see below). |
| `mcphub_stats` | Local usage intelligence: calls, errors, estimated token cost, per-server breakdown. |

The gateway's MCP instructions tell the connecting model it is in lazy mode
and that the underlying tools *are* available — so a capable agent discovers
and calls tools proactively without you prompting it to.

## The discovery loop

A lazy-mode agent works the catalog in three steps.

**1. Discover.** Search by keyword:

```json
// mcphub_search_tools
{ "query": "semantic search" }
```

The response lists matches with their `namespaced` (`server__tool`) name,
server, tool, and description.

**2. Inspect.** Two options, depending on how much the agent already knows:

- `mcphub_describe_tool` takes `{server, tool}` — or just `tool` in the
  combined `server__tool` form — and returns the tool's description and full
  JSON `input_schema`, enough to construct a valid call.
- `mcphub_resolve_tool` collapses search + describe into one round trip: give
  it a natural-language `query` (and optionally `max_hits`, default 5) and it
  returns one recommendation with `required_fields` and a ready-to-fill
  `argument_template`, a list of alternatives, and an `ambiguous` flag when
  several tools ranked equally.

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
  the seven meta-tools plus the indirection can cost as much as just mounting
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

- [Gateway meta-tools](/reference/meta-tools) — full reference for all seven
  meta-tools, including their exact call shapes.
- [Bounded, lossless results](/guide/results) — the `callId` receipt and
  `mcphub_get_result` paging contract behind oversized calls.
- [Per-agent routing](/guide/routing) — scoping servers and tools per agent,
  which also filters what a lazy-mode agent can discover and pin.
- [Configuration reference](/reference/config) — the `expose` and `pin` fields.
