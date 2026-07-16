---
title: Concepts
description: "How mcphub works: one mcphub.yaml registry, a stdio gateway that namespaces tools as server__tool, gateway vs direct modes, lazy exposure, and its meta-tools."
---

# Concepts

mcphub is two things wearing one config: a **gateway** that fronts many MCP
servers behind a single connection, and a **control plane** that keeps your
agent harnesses in sync. This page explains the ideas that make both work.

## The single source of truth

Everything starts from `mcphub.yaml`. It declares:

- **`servers`** — every downstream MCP server mcphub can manage and proxy,
  stdio (a `command`) or remote (a `url` with `transport: http` or `sse`).
- **`groups`** — optional named bundles of servers (`mcphub use <group>`).
- **`agents`** — the 11 agent harnesses mcphub keeps in sync (Claude Code,
  opencode, Codex, Copilot CLI, Qwen Code, Gemini CLI, Kilo Code, Kimi Code
  CLI, Crush, Forge, Hermes), each with a `path` and a `mode`.

You edit this one file (or toggle servers in [Studio](/guide/studio)), and
mcphub propagates the result everywhere. You never hand-edit `~/.claude.json`,
`opencode.json`, and `~/.codex/config.toml` again.

::: tip Already have servers configured?
`mcphub init --from-agents` scans your installed harness configs, unions every
MCP server they already declare into `mcphub.yaml`, and wires those agents up
in gateway mode — you adopt mcphub without retyping anything.
:::

## The gateway: one connection, N servers

`mcphub mcp serve` is mcphub's own MCP stdio server — the single endpoint an
agent points at. When it starts it:

1. Reads and validates `mcphub.yaml` and connects, **concurrently**, to every
   *enabled* downstream server admitted by the optional `--agent` server scope.
   Excluded servers are not spawned, contacted, or asked to resolve secrets. A
   stdio server is spawned as a subprocess; a remote server is reached over
   HTTP or SSE.
2. Lists each downstream's tools and **mounts** them onto its own server.
3. Serves on stdio, recording every proxied call to the local
   [intelligence store](/guide/intelligence).

A downstream that fails to start is recorded with its error and skipped — it
never aborts the whole gateway. The remaining servers stay available.

```
                 ┌─────────────────────── mcphub mcp serve ───────────────────────┐
   one agent ──▶ │  gateway MCP server                                             │
   (stdio)       │   ├─ codemap__codemap_find      ─┐                              │
                 │   ├─ codemap__codemap_impact      ├─▶ codemap   (stdio child)   │
                 │   ├─ vecgrep__vecgrep_search     ─┼─▶ vecgrep   (stdio child)   │
                 │   ├─ memory__memory_recall       ─┼─▶ memory    (remote http)   │
                 │   └─ mcphub_list_servers / ...    ┘   (meta-tools)              │
                 └─────────────────────────────────────────────────────────────────┘
```

The gateway's logs go to **stderr**; stdout carries only the JSON-RPC stream,
so running `mcphub mcp serve` by hand for debugging never corrupts the
protocol.

### Namespacing: `server__tool`

To keep tool names unique across servers, the gateway exposes each downstream
tool under a **namespaced** name: the server's name, two underscores, then the
tool's original name. A tool named `search` on a server named `vecgrep` becomes
`vecgrep__search`. Names never collide, and you always know which server a tool
came from — the description is prefixed with `[server]` too. The gateway
preserves the downstream title, input and output schemas, annotations, icons,
and `_meta`, so clients retain the tool's display, validation, and safety hints
after namespacing.

When an agent calls `vecgrep__search`, the gateway relays the arguments to the
real `search` tool on the `vecgrep` session unchanged, times the call, and
records it. Small results pass through unchanged. If the complete serialized
result exceeds `response_budget`, mcphub stores it for 24 hours in the local
SQLite database (directory `0700`, database `0600`) and returns a compact
recovery receipt instead of dropping bytes. Set `verbatim: true` or
`response_budget: "0"` on a server for transparent, unbounded pass-through. See
[Bounded, lossless results](/guide/results) for the full recovery flow.

### The eight meta-tools

Beyond the proxied tools, the gateway registers eight management tools of its
own so an agent can introspect and drive the hub without scanning everything:

- **`mcphub_list_servers`** — configured servers with their enabled/connected
  state, tool counts, and the current exposure mode.
- **`mcphub_search_tools`** — rank natural-language intent across tool metadata
  plus server descriptions, tags, and `use_when` hints, returning bounded
  `server__tool` candidates with match evidence.
- **`mcphub_describe_tool`** — return one downstream tool's description and
  full input schema.
- **`mcphub_resolve_tool`** — route a current goal/activity and return one
  recommended tool, match evidence, required fields, an argument template,
  alternatives, and ambiguity status.
- **`mcphub_call_tool`** — invoke any downstream tool by
  `{server, tool, arguments}`. Oversized results return a lossless recovery
  receipt; `detach: true` runs a long-running tool in the background and
  returns a `callId` immediately, and `timeout_ms` bounds the call.
- **`mcphub_get_result`** — recover a stored result by `callId` and zero-based
  byte `cursor`. Decode each base64 `data` page and continue with `nextCursor`
  until `done` is true.
- **`mcphub_poll_result`** — check a detached call by `callId`: `pending` while
  it runs, `failed` with the error, or the finished tool result itself.
- **`mcphub_stats`** — local usage intelligence: total calls, errors, estimated
  token cost, and a per-server breakdown.

## Exposure: `all` vs. `lazy`

The top-level `expose` key in `mcphub.yaml` controls how many tools the gateway
advertises (see [Lazy mode](/guide/lazy-mode) for the deep dive):

- **`expose: all`** (default) — every downstream tool is mounted as
  `server__tool`. Simple, but a large fleet means a large tool list.
- **`expose: lazy`** — only the eight meta-tools above are advertised. The
  agent routes task context with `mcphub_resolve_tool`, browses alternatives
  with `mcphub_search_tools`, and runs the choice with `mcphub_call_tool`.
  Initialization includes a bounded capability summary built from the agent's
  in-scope servers and their `use_when` hints. The context cost is a handful of
  tools instead of hundreds.

The trade-off of lazy mode: because the real tools aren't in the agent's tool
list, the model still has to follow the gateway instructions and call the
resolver/search entry point. `use_when` makes that decision substantially more
discoverable, but a harness that ignores MCP instructions may still need pins
or `expose: all`. Pins keep tools mounted
directly even in lazy mode, so they appear in the agent's tool list and get
auto-invoked, while everything else stays on-demand:

```yaml
expose: lazy
pin:
  - codemap                       # a whole server — all its tools, auto-callable
  - vecgrep__*                    # same, explicit wildcard
  - tinyvault__vault_get_secret   # a single tool
```

Manage pins without editing YAML: `mcphub pin codemap vecgrep`,
`mcphub unpin codemap`, or `p` on a server in [Studio](/guide/studio). And
`mcphub pin --top 8` auto-pins your eight most-called tools straight from the
[intelligence store](/guide/intelligence) — let your real usage decide what's
always-on. The sweet spot: lazy everywhere, pin the two or three servers you
live in.

::: tip Pins need no sync
In gateway mode a pin change takes effect the next time the gateway starts —
just restart your agents to pick it up. No `mcphub sync` required.
:::

## Gateway vs. direct

Each agent in `mcphub.yaml` has a **`mode`** that controls what `mcphub sync`
writes into it.

### `mode: gateway` (default)

mcphub writes **only one** server into the agent — `mcphub`, pointing at
`mcphub mcp serve`. The agent sees a single MCP server; mcphub proxies all the
real servers behind it.

```yaml
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway   # the agent sees ONLY mcphub
```

### `mode: direct`

mcphub writes **every enabled server** straight into the agent's config,
verbatim — same command, args, env, url, and transport as in `mcphub.yaml`.
There is no gateway hop; the agent connects to each server itself.

```yaml
agents:
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: direct    # every enabled server, written in directly
```

Mix modes freely: one agent can run through the gateway while another talks to
servers directly.

::: warning Sync never surprises you
`mcphub sync` is dry-run by default — it prints the exact diff and changes
nothing until you pass `--write`, and even then it saves a timestamped `.bak`
first and only touches the entries it owns. See [Sync](/guide/sync).
:::

## Per-agent routing

Modes and `expose` are global knobs. For finer control — "Codex gets only
codemap and vecgrep; Claude gets everything" — give an agent a `servers` and/or
`tools` allowlist (full reference: [Per-agent routing](/guide/routing)):

```yaml
agents:
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    servers: [codemap, vecgrep]                # only these enabled servers
    tools: [codemap__codemap_find, vecgrep__vecgrep_search]  # gateway-only
```

- **`servers`** — which enabled downstream servers the agent may reach.
  Omit it for all enabled servers; an explicit empty list `[]` means **none**
  (a deliberately minimal agent). In direct mode only those servers are
  written; in gateway mode the spawned `mcphub mcp serve --agent <name>`
  proxies only them.
- **`tools`** — which `server__tool` names a gateway-mode agent may call (each
  must belong to one of the allowed `servers`). Omit for every tool of the
  allowed servers; an explicit empty list `[]` means **none**. Direct mode
  can't filter individual tools (the agent talks to each server itself), so
  `tools` is rejected there.

The gateway does not activate servers outside the server allowlist, refuses
out-of-scope calls with a clear error, and `mcphub doctor` reports each agent's
scope (`routes to N/M enabled servers`). This provides least activation and
context **curation**, not operating-system security isolation.

## Token savings

This is the practical payoff of gateway mode.

Every MCP server an agent connects to contributes its **entire tool list** to
the model's context on every request — names, descriptions, and JSON schemas.
A dozen servers can be hundreds of tool definitions loaded before you type a
single word.

In gateway mode the agent loads exactly **one** server. With `expose: lazy`
that surface collapses to eight meta-tools plus a bounded capability summary
no matter how many servers sit behind the hub — the model sees
`mcphub_resolve_tool` / `mcphub_call_tool` instead of every server's full
catalog, and pulls a tool's schema on demand only when it actually needs it.
(With the default `expose: all`, you still get
one connection, but the full catalog is advertised under `server__tool` names.)

If an agent already had those servers configured directly, adding the gateway
alone doesn't shrink anything — the agent is carrying both. That's what
`mcphub offload` is for: after `mcphub sync --write` has put the gateway in
place, `mcphub offload --write` removes the direct copies of the servers mcphub
now proxies from each gateway-mode agent, leaving just `mcphub`. It only
removes entries mcphub both proxies **and** previously managed, so hand-added
servers survive; it is dry-run by default like sync.

```sh
mcphub sync --write       # give every agent the mcphub gateway
mcphub offload            # preview which direct entries would be removed
mcphub offload --write    # apply — this is where the token savings land
```

mcphub also *measures* this. Every proxied call records an estimated token cost
(a cheap bytes-per-token heuristic over the request and response), so
[`mcphub stats`](/guide/intelligence) can tell you which servers actually earn
their place in your context window — and which you might disable.

## Architecture in plain words

The **config** layer reads `mcphub.yaml` — the registry every other piece
consults. The **hub** connects to each enabled, in-scope server as an MCP client,
discovers its tools, and re-exposes them under `server__tool` names, forwarding
calls transparently and timing each one. The **MCP server** layer adds the
eight meta-tools and serves everything on one stdio connection. The **store**
persists every call (and any oversized results) to a local SQLite database.
The **harness adapters** turn mcphub's view of servers into each agent's
on-disk config format, and the **syncer** reconciles the two. Each piece is
small and does one thing; together they are the hub.

## Next

- [Sync to your agents](/guide/sync) — how each harness adapter merges.
- [Lazy mode](/guide/lazy-mode) — the full walkthrough of `expose: lazy` and pinning.
- [Per-agent routing](/guide/routing) — scoping servers and tools per agent in detail.
- [Bounded, lossless results](/guide/results) — how oversized results are spooled and recovered.
- [Intelligence](/guide/intelligence) — the telemetry and SQLite store.
- [Configuration reference](/reference/config) — every field of `mcphub.yaml`.
