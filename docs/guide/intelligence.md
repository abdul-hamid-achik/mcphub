---
title: Intelligence
description: mcphub records every proxied MCP tool call in a local SQLite database. Use mcphub stats and mcphub status to see which servers earn their context budget.
---

# Intelligence

mcphub keeps a small **local intelligence layer**: every tool call the gateway
proxies is recorded to a SQLite database on your machine. That data powers
`mcphub stats`, `mcphub status`, `mcphub pin --top`, and the Studio Stats tab,
so you can see which servers and tools actually earn their place in your
context window — and which you can disable.

This is the part of mcphub that turns "I connected a dozen MCP servers" into
"these three get used, that one errors half the time, and this one is quietly
costing me a fortune in tokens."

## What gets recorded

When an agent calls a proxied tool through the gateway, the hub forwards the
call, times it, and writes one row capturing:

- the downstream **server** and **tool** names, plus the **namespaced** name
  (`server__tool`) the gateway exposed,
- the **duration** in milliseconds,
- whether it **succeeded or errored** (and the error text, if any),
- the **size** of the request arguments and the response, in bytes,
- an **estimated token cost**.

Recording happens on the proxy path but never blocks it: if a telemetry write
fails, the gateway logs a warning and keeps serving.

::: warning Only proxied calls are recorded
Intelligence covers calls that go **through the gateway**. An agent in
`direct` mode talks to each server itself, so its calls never pass through
mcphub and are not recorded. If you want the numbers, run the agent in
`gateway` mode — see [Concepts](/guide/concepts).
:::

### The token estimate

The estimated token cost is a deliberately cheap heuristic — roughly **four
bytes per token** over the combined request and response size. It is not a
billing-grade count and it isn't tied to any specific model's tokenizer. Its job
is **relative comparison**: it's good enough to tell you that one server costs an
order of magnitude more context than another, which is exactly the decision you
care about when deciding what to keep enabled.

## Viewing stats

```sh
mcphub stats
```

```
Totals (all time): 128 calls, 3 errors, ~41210 tokens, 9874ms total

SERVER    CALLS  ERRORS  AVG_MS  EST_TOKENS
codemap   71     0       54      28110
vecgrep   42     1       120     11300
memory    15     2       38      1800
```

The header is the all-time rollup (calls, errors, estimated tokens, total
milliseconds). The table breaks it down per server, **sorted by call count**,
with average latency and estimated token cost. If nothing has been recorded yet,
mcphub tells you so and reminds you to point an agent at `mcphub mcp serve` and
use it.

Flags drill in further:

```sh
mcphub stats --tools            # per-tool breakdown — which exact tools cost the most
mcphub stats --recent 20        # also list the 20 most recent calls
mcphub stats --since 7d         # scope to a recent window (24h, 90m, 7d, ...)
mcphub stats --server vecgrep   # drill into one server's stats and tools
```

`--since` is the actionable one: all-time totals tell you what you've *ever*
used, but `--since 7d` tells you what's earning its context budget **now** — so
a server that was useful last month but unused this week shows up as a
disable candidate.

For sharing or scripting, the same report renders as Markdown or JSON:

::: code-group

```sh [Markdown]
mcphub stats --markdown         # paste into notes, a PR, or an issue
```

```sh [JSON]
mcphub stats --json             # totals + per-server and per-tool breakdowns
```

:::

The gateway also exposes the same intelligence as a meta-tool, `mcphub_stats`,
so an agent can introspect its own usage mid-session.

## Drift and unused servers: `mcphub status`

[`mcphub status`](/reference/cli#status) answers "is everything consistent?" by
fusing this intelligence with sync state. For each agent it does a **read-only
dry run** and reports whether the agent's config already matches `mcphub.yaml`
("in sync") or has changes pending — per-agent **sync drift**, without touching
anything. It then summarizes recorded usage and flags **enabled servers that
have never been called**. Those are dead weight in every agent's context, so
`status` suggests disabling them:

```
Usage:   142 calls, 3 errors, ~38500 est. tokens
Unused:  monitor, vidtrace (enabled but never called)
         → consider `mcphub disable <name>` to shrink agent context.
```

Use `--markdown` for a report you can paste into notes or an issue, `--json`
for a machine-readable one, and `--server <name>` to scope the report to one
server — which agents route to it, plus its proxied-call count:

```sh
mcphub status --markdown
mcphub status --server vecgrep
```

## Deciding what earns its context budget

The feedback loop that makes the hub *smart*:

1. **Work normally** for a few days with your agents pointed at the gateway.
2. **Check the recent window** — `mcphub stats --since 7d` shows what's pulling
   its weight right now; `--tools` shows which exact tools drive the cost.
3. **Ask status for candidates** — `mcphub status` flags enabled-but-unused
   servers.
4. **Trim** — `mcphub disable <name>` for the dead weight, then
   `mcphub sync --write` to push the leaner set to every agent
   (see [Sync](/guide/sync)).

## Pinning from stats: `pin --top`

Intelligence pairs directly with [lazy exposure](/guide/concepts). Under
`expose: lazy` the gateway advertises only its eight meta-tools and agents
discover the rest on demand — a big token saving, at the cost of one discovery
hop. `mcphub pin` keeps chosen tools mounted directly so agents call them
without going through `mcphub_search_tools` first, and `--top` picks them
**from the recorded call counts**:

```sh
mcphub pin --top 8    # auto-pin your 8 most-called tools from the intelligence store
mcphub pin            # list current pins
```

The result is the best of both: rarely-used tools stay lazy, and the tools you
demonstrably call most stay one hop away. In gateway mode no sync is needed —
the change takes effect the next time the gateway starts, so restart your
agents to pick it up.

## The store

The database is a single-file SQLite database driven by a pure-Go driver
(`modernc.org/sqlite`, no cgo — the binary stays self-contained). It lives at:

```
~/.local/share/mcphub/mcphub.db
```

following the XDG data convention. Override it with the `--db` flag or the
`MCPHUB_DB` environment variable. The schema is created and migrated
automatically the first time the store is opened.

### Schema

Four tables, all local to you:

- **`tool_calls`** — one row per proxied tool call: timestamp, server, tool,
  namespaced name, duration, ok/error, arg and result byte sizes, and the token
  estimate. Indexed by server and by timestamp. This is what `stats` aggregates.
- **`managed_entries`** — which servers mcphub has written into which agent
  harness. This is the bookkeeping that lets [`sync`](/guide/sync) and
  `offload` prune entries mcphub previously owned without touching servers you
  added by hand.
- **`sync_runs`** — an audit log of every `mcphub sync`: timestamp, agent, mode,
  the server set, and whether it was a dry run.
- **`result_spool`** — exact serialized copies of oversized MCP results, kept
  for 24 hours under opaque call IDs so agents can recover them in bounded
  pages with `mcphub_get_result`.

The typed query layer is generated by [sqlc](https://sqlc.dev) from the SQL in
the repository (`task sqlc` regenerates it); a hand-written wrapper exposes the
ergonomic methods the rest of mcphub calls.

## Doctor and the store

`mcphub doctor` includes a `store` check that confirms the database opens
successfully (and reports its path), alongside its checks on your config, your
enabled servers' commands, and your agents' config files. See
[`mcphub doctor`](/reference/cli#doctor).

## Next

- [CLI reference](/reference/cli#stats) — the full `stats` and `status` surface.
- [Studio](/guide/studio) — the same data in the TUI.
- [Concepts](/guide/concepts) — gateway mode, lazy exposure, and why the token
  estimate matters.
