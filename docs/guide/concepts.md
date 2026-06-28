# Concepts

mcphub is two things wearing one config: a **gateway** that fronts many MCP
servers behind a single connection, and a **control plane** that keeps your
agent harnesses in sync. This page explains the ideas that make both work.

## The single source of truth

Everything starts from `mcphub.yaml`. It declares:

- **`servers`** — every downstream MCP server mcphub can manage and proxy,
  stdio (a `command`) or remote (a `url`).
- **`groups`** — optional named bundles of servers.
- **`agents`** — the harnesses mcphub keeps in sync (Claude Code, opencode,
  Codex), each with a `path` and a `mode`.

You edit this one file (or toggle servers in [Studio](/guide/studio)), and
mcphub propagates the result everywhere. You never hand-edit `~/.claude.json`,
`opencode.json`, and `~/.codex/config.toml` again.

## The gateway: one connection, N servers

`mcphub mcp serve` is mcphub's own MCP stdio server — the single endpoint an
agent points at. When it starts it:

1. Reads `mcphub.yaml` and connects, **concurrently**, to every *enabled*
   downstream server as an MCP client. A stdio server is spawned as a
   subprocess; a remote server is reached over HTTP or SSE.
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
                 │   └─ mcphub_list_servers / ...    ┘   (management tools)        │
                 └─────────────────────────────────────────────────────────────────┘
```

### Namespacing: `server__tool`

To keep tool names unique across servers, the gateway exposes each downstream
tool under a **namespaced** name: the server's name, two underscores, then the
tool's original name. A tool named `search` on a server named `vecgrep` becomes
`vecgrep__search`. The description is prefixed with `[server]` so agents can see
where a tool comes from, and the original input schema is passed through
unchanged.

When an agent calls `vecgrep__search`, the gateway relays the arguments to the
real `search` tool on the `vecgrep` session **verbatim**, times the call,
records it, and returns the downstream result unchanged. It is a transparent
passthrough — mcphub does not rewrite your arguments or results.

### Management tools

Beyond the proxied tools, the gateway exposes five of its own so an agent can
introspect and drive the hub without scanning everything:

- **`mcphub_list_servers`** — configured servers with their enabled/connected
  state, tool counts, and the current exposure mode.
- **`mcphub_search_tools`** — search the aggregated catalog by substring across
  tool name and description, returning matching `server__tool` names. Lets an
  agent find a capability without loading every tool.
- **`mcphub_describe_tool`** — a downstream tool's description and full JSON
  input schema, so the agent can construct a valid call.
- **`mcphub_call_tool`** — invoke any downstream tool by `{server, tool,
  arguments}` and get its result verbatim. This is how tools are called in lazy
  mode.
- **`mcphub_stats`** — local usage intelligence: total calls, errors, estimated
  token cost, and a per-server breakdown.

## Exposure: `all` vs. `lazy`

The top-level `expose` key in `mcphub.yaml` controls how many tools the gateway
advertises:

- **`expose: all`** (default) — every downstream tool is mounted as
  `server__tool`. Simple, but a large fleet means a large tool list.
- **`expose: lazy`** — only the five meta-tools above are advertised. The agent
  finds a capability with `mcphub_search_tools`, optionally inspects it with
  `mcphub_describe_tool`, and runs it with `mcphub_call_tool`. The context cost
  is a handful of tools instead of hundreds — regardless of how many servers are
  behind the hub.

The trade-off of lazy mode: because the real tools aren't in the agent's tool
list, the model won't *automatically* reach for them — it has to choose to call
`mcphub_search_tools` first. So if you want a server's tools called
automatically (like a normal MCP setup), **pin it**. Pins keep tools mounted
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

## Token savings

This is the practical payoff of gateway mode.

Every MCP server an agent connects to contributes its **entire tool list** to
the model's context on every request — names, descriptions, and JSON schemas.
A dozen servers can be hundreds of tool definitions loaded before you type a
single word.

In gateway mode the agent loads exactly **one** server. With `expose: lazy`
that surface collapses to just five meta-tools no matter how many servers sit
behind the hub — the model sees `mcphub_search_tools` / `mcphub_call_tool`
instead of every server's full catalog, and pulls a tool's schema on demand
only when it actually needs it. (With the default `expose: all`, you still get
one connection, but the full catalog is advertised under `server__tool` names.)

mcphub also *measures* this. Every proxied call records an estimated token cost
(a cheap bytes-per-token heuristic over the request and response), so
[`mcphub stats`](/guide/intelligence) can tell you which servers actually earn
their place in your context window — and which you might disable.

## Proxy architecture in one paragraph

The hub connects to each enabled server, discovers its tools, and re-exposes
them under `server__tool` names on a single MCP server, forwarding calls
transparently and timing each one. The MCP server layer adds the management
tools and serves on stdio. The store layer persists every call to SQLite. The
harness layer turns mcphub's view of servers into each agent's on-disk config
format. The config layer ties it together as `mcphub.yaml`. Each piece is small
and does one thing; together they are the hub.

## Next

- [Sync to your agents](/guide/sync) — how each harness adapter merges.
- [Intelligence](/guide/intelligence) — the telemetry and SQLite store.
- [Configuration reference](/reference/config) — every field of `mcphub.yaml`.
