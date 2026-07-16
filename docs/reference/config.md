---
title: Configuration reference
description: Complete mcphub.yaml reference — servers, use_when discovery hints, groups, agents, expose, pin, response budgets, secrets, and routing.
---

# Configuration reference

The mcphub config file is the single source of truth for which downstream MCP
servers exist, how they group, and which agent harnesses mcphub keeps in sync.
Edit this one file (or use [Studio](/guide/studio)), then
[`mcphub sync`](/guide/sync) propagates the result into every agent.

## Format: YAML, TOML, or JSON

The config can be **YAML** (`mcphub.yaml`, the default and the only one that
keeps inline comments), **TOML** (`mcphub.toml`), or **JSON** (`mcphub.json`).
mcphub picks the format from the file extension and reads and writes all
three — so `enable`, `disable`, `add`, and Studio round-trip in whatever
format you chose.

Generate one with `mcphub init`, choosing a format explicitly with `--format`:

```sh
mcphub init                    # ~/.config/mcphub/mcphub.yaml (default)
mcphub init --format toml      # mcphub.toml
mcphub init --format json      # mcphub.json
mcphub init --from-agents      # union servers from installed harness configs, instead of a blank starter
mcphub init --force            # overwrite an existing config
```

`--from-agents` scans your installed harness configs (Claude Code, opencode,
Codex, Crush, Forge, Hermes), unions every MCP server they already declare,
and wires those agents up in `gateway` mode — so you can adopt mcphub without
retyping servers you already have.

## Location

mcphub resolves the config path in this order:

1. the `--config <path>` flag,
2. the `MCPHUB_CONFIG` environment variable,
3. the first existing `mcphub.{yaml,yml,toml,json}` in the current directory,
4. the first existing one in `~/.config/mcphub/`, else `~/.config/mcphub/mcphub.yaml`.

The same precedence (flag, then env var, then default) applies to the
intelligence database: `--db`, then `MCPHUB_DB`, then
`~/.local/share/mcphub/mcphub.db`.

::: tip
Every command accepts `--config`, `--db`, and `--json` as persistent flags —
see the [CLI reference](/reference/cli) for the full surface.
:::

## Top-level shape

```yaml
version: 1
expose: all             # or: lazy
response_budget: 32KB   # complete serialized MCP result budget; 0 = unlimited
verbatim: false         # true = never spool or replace downstream results
connect_timeout: 30s    # per-downstream connect timeout (default 30s)
call_timeout: 30m       # clamps timeout_ms and bounds detached calls; sync calls without timeout_ms follow the client's deadline
pin:                    # tools always mounted, even in lazy mode (optional)
  - codemap__codemap_semantic

servers:
  # name -> server definition
  <name>: { ... }

groups:
  # optional named bundles of server names
  <name>: [<server>, ...]

agents:
  # name -> agent (harness) definition
  <name>: { ... }
```

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `version` | int | yes | Config schema version. Currently `1`. |
| `expose` | `all` \| `lazy` | no | Gateway tool exposure. `all` (default) mounts every downstream tool as `server__tool`; `lazy` advertises only mcphub's eight meta-tools and serves the rest on demand via `mcphub_call_tool`. See [Lazy mode](/guide/lazy-mode). |
| `pin` | list of strings | no | Tools that stay mounted even in `lazy` mode, so agents call them directly instead of discovering them first. Each entry is a bare server (`codemap` — all its tools), a whole-server wildcard (`codemap__*`), or one tool (`codemap__codemap_semantic`) — those are the only shapes; any other wildcard or a trailing `__` fails validation, as does a pin naming an unknown server. Manage with `mcphub pin` / `mcphub unpin` (`--top N` auto-pins your N most-called tools from `mcphub stats`), or `p` in Studio. |
| `response_budget` | byte-size string | no | Complete serialized MCP result budget. Default `32KB`; `"0"` is unlimited. A non-zero value must be at least `512B` so a recovery receipt can fit. Oversized results are stored locally for 24 hours and recovered with `mcphub_get_result`. |
| `verbatim` | bool | no | Return every downstream result unchanged and disable result spooling entirely. Default `false`. |
| `connect_timeout` | duration string | no | Per-downstream connect timeout, e.g. `30s`, `60s`, `2m`. Default `30s`. |
| `call_timeout` | duration string | no | Gateway-side call bound, e.g. `10m`, `30m`, `1h`. Default `30m`. Clamps a caller's `timeout_ms` and bounds how long a detached (`detach: true`) call may run in the background. It is **not** a blanket ceiling: a synchronous `mcphub_call_tool` without `timeout_ms`, and any directly-mounted (`expose: all`) tool call, is bounded only by the client's own request deadline. |
| `servers` | map | yes | The downstream MCP servers mcphub manages. |
| `groups` | map | no | Named bundles of server names. |
| `agents` | map | yes | The agent harnesses mcphub keeps in sync. |

::: tip Bounded, lossless results
Small results always pass through unchanged. A result over `response_budget`
is stored losslessly in SQLite for 24 hours and replaced with a compact
recovery receipt; the agent recovers the exact serialized payload in bounded
pages with `mcphub_get_result`. See [Bounded, lossless results](/guide/results).
:::

---

## `servers`

A map of server name to definition. Each server is **either** a local stdio
server (`command`) **or** a remote server (`url`) — exactly one of the two.

```yaml
servers:
  # stdio server
  codemap:
    command: codemap
    args: [serve]
    env:
      LOG_LEVEL: info
    enabled: true
    description: Code knowledge graph
    tags: [code, search]
    use_when:
      - understand symbols, references, and structure in a codebase
      - trace callers or dependencies before changing code
    tool_use_when:
      codemap_impact:
        - estimate the blast radius of changing one symbol

  # remote server, with a secret injected from tvault at connect time
  obsidian:
    url: "https://127.0.0.1:27124/mcp"
    transport: http
    headers:
      Authorization: "tvault://obsidian/authorization"
    enabled: false
    description: Obsidian Local REST API
```

### Fields

| Field | Type | Applies to | Description |
| --- | --- | --- | --- |
| `command` | string | stdio | The executable to spawn for a local stdio server. |
| `args` | list of strings | stdio | Arguments passed to `command`. |
| `env` | map of string→string | stdio | Environment variables for the spawned process (merged over the inherited environment). |
| `url` | string | remote | The endpoint of a remote server. |
| `transport` | string | remote | `http` (default, streamable HTTP) or `sse`. |
| `headers` | map of string→string | remote | Custom HTTP headers sent with every request. A value that starts with `tvault://` is resolved to a secret at connect time instead of read literally — see [Secrets](/guide/secrets#tvault-refs-secrets-in-remote-headers). |
| `vault` | string | stdio | A [TinyVault](https://github.com/abdul-hamid-achik/tinyvault) (`tvault`) project. The server is launched via `tvault run --project <vault> -- <command>`, injecting that project's secrets as env vars at spawn — so they never live in `mcphub.yaml`. |
| `vault_only` | list of strings | stdio | Inject only these secret keys (least-privilege allowlist). |
| `vault_prefix` | string | stdio | Inject only secret keys with this prefix. |
| `enabled` | bool | both | Whether the gateway connects to it and `sync` (direct mode) writes it. |
| `description` | string | both | Human-readable note shown in `list`, Studio, and `mcphub_list_servers`. |
| `tags` | list of strings | both | Free-form labels. |
| `use_when` | list of strings | both | Natural-language situations in which this server is useful. `mcphub_resolve_tool` and `mcphub_search_tools` index these hints so lazy-mode agents can find unpinned tools even when their names are opaque. Up to 8 non-empty, single-line UTF-8 hints, 256 bytes each. |
| `tool_use_when` | map of tool name to list of strings | gateway | Higher-precision situations for individual tools. These receive more routing weight than server-wide hints. Up to 128 tool entries and 8 bounded hints per tool. |

### Rules (validated on load)

- The name `mcphub` is reserved (it's the gateway entry `sync` writes into
  agents), and a server name must not contain `__` — that's the namespacing
  separator in `server__tool`.
- A server must set **either** `command` **or** `url` — not both, and not
  neither.
- `transport`, if set, must be `http` or `sse`. For a remote server with no
  `transport`, mcphub uses streamable HTTP.
- `headers` only makes sense on a remote server; a server with `headers` set
  but no `url` fails validation with `headers only apply to remote (url) servers`.
- `vault` requires a `command` (it wraps a spawned process) — it can't be used
  with a remote `url`.
- Only **enabled** servers are connected by the gateway and written by `sync`
  in direct mode.
- `use_when` accepts at most 8 non-empty, single-line UTF-8 hints of at most 256 bytes each.
  Write them as outcomes or situations (for example, “capture a URL as clean
  Markdown”), not as tool names; tool names and descriptions are indexed
  automatically.
- `tool_use_when` uses the same hint bounds and accepts at most 128 tool names
  per server. Unknown names remain inert until the downstream exposes a matching
  tool, which lets configuration survive optional or versioned capabilities.

### stdio vs. remote

A server with a `command` is a **stdio** server: the gateway spawns it as a
subprocess and speaks MCP over its stdin/stdout, with any `env` merged over
the inherited environment. A server with a `url` is **remote**: the gateway
connects over the network using the chosen `transport` (`http` streamable, or
`sse`).

### Secrets: `vault` and `tvault://` headers

Two independent mechanisms keep secrets out of `mcphub.yaml`, both backed by
[TinyVault](https://github.com/abdul-hamid-achik/tinyvault) (`tvault`):

```yaml
servers:
  # vault: for stdio servers — secrets injected as env vars at spawn
  github:
    command: gh-mcp
    args: [--stdio]
    vault: github            # spawn via `tvault run --project github`
    vault_only: [GH_TOKEN]   # least-privilege allowlist (optional)
    enabled: true

  # tvault:// headers — for remote servers, resolved at connect time
  obsidian:
    url: "https://127.0.0.1:27124/mcp"
    transport: http
    headers:
      Authorization: "tvault://obsidian/authorization"
    enabled: true
```

- **`vault`** wraps the spawn command as `tvault run --project <vault>
  [--only <vault_only>] [--prefix <vault_prefix>] -- <command> <args...>`. In
  **gateway** mode the hub spawns it, so the secrets never reach the agent at
  all. In **direct** mode `sync` writes the *wrapped* command into the
  agent's config verbatim, so `tvault` must be on the agent's own `PATH` too.
- **`headers`** values starting with `tvault://<project>/<key>` (or
  `tvault://<key>` for the active project) are resolved by shelling out to
  `tvault get` when the gateway dials the server. This only happens in
  **gateway** mode — `sync` never writes `headers` into any agent's config,
  so a direct-mode agent never sees the header, resolved or literal.

`mcphub doctor` checks that `tvault` is on `PATH` whenever a server uses
`vault`. Add a vaulted stdio server from the CLI with:

```sh
mcphub add github gh-mcp --vault github --vault-only GH_TOKEN
```

There is no `--vault-prefix` or header-setting flag on `mcphub add` yet — set
`vault_prefix` and `headers` by editing the config directly. See
[Secrets](/guide/secrets) for the full walkthrough, including the validation
errors and the direct-mode caveats for each mechanism.

---

## `groups`

Optional named bundles of server names — a convenience for organizing related
servers.

```yaml
groups:
  coding: [codemap, vecgrep]
```

Every member must be the name of a server defined under `servers`; an unknown
member fails validation. Flip a whole bundle on at once with
`mcphub use <group>` (add `--only` to also disable every server not in the
group), then run `mcphub sync` to apply. `mcphub groups` lists what's defined.

---

## `agents`

A map of agent name to the harness mcphub syncs into. Each agent points at a
config file, chooses a sync mode, and can optionally be scoped to a subset of
servers and tools.

```yaml
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: direct
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    # disabled: true                 # skip during sync without deleting the definition
    # servers: [codemap, vecgrep]    # only these enabled servers (omit = all; [] = none)
    # tools: [codemap__codemap_find] # gateway-only: only these server__tool names (omit = all; [] = none)
  local-agent:
    type: local-agent
    path: ~/.config/local-agent/config.yaml
    mode: gateway
```

### Fields

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `type` | string | yes | The harness adapter: `claude`, `opencode`, `codex`, `crush`, `forge`, `hermes`, `copilot`, `qwen`, `gemini`, `kilo`, `kimi`, or `local-agent`. |
| `path` | string | yes | The harness config file. Supports leading `~` expansion. |
| `mode` | string | no | `gateway` (default) or `direct`. |
| `disabled` | bool | no | Skip this agent during `sync` without deleting its definition. |
| `servers` | list of strings | no | Which enabled downstream servers this agent may reach. Omitted means all; an explicit `[]` means none. |
| `tools` | list of strings | no | Which `server__tool` names this agent may call. **Gateway mode only.** Omitted means every tool of the allowed servers; an explicit `[]` means none. |

### `type`

Selects the file-format adapter that translates mcphub's view of servers into
the harness's on-disk format:

| `type` | Target file (typical) | MCP section |
| --- | --- | --- |
| `claude` | `~/.claude.json` | `mcpServers` |
| `opencode` | `~/.config/opencode/opencode.json` | `mcp` |
| `codex` | `~/.codex/config.toml` | `[mcp_servers.*]` |
| `crush` | `~/.config/crush/crush.json` | `mcp` |
| `forge` | `~/forge/.mcp.json` | `mcpServers` |
| `hermes` | `~/.hermes/config.yaml` | `mcp_servers` |
| `copilot` | `~/.copilot/mcp-config.json` | `mcpServers` |
| `qwen` | `~/.qwen/settings.json` | `mcpServers` |
| `gemini` | `~/.gemini/settings.json` | `mcpServers` |
| `kilo` | `~/.config/kilo/kilo.jsonc` | `mcp` |
| `kimi` | `~/.kimi/config.toml` | `[mcp_servers.*]` |
| `local-agent` | `~/.config/local-agent/config.yaml` | YAML `servers` sequence |

See [Supported harnesses](/guide/harnesses) for each adapter's format details,
including the note that the Codex and Kimi TOML round-trips do not preserve
comments or key ordering, and that Kilo's JSONC parser strips comments on
write. `mcphub agents` shows which harnesses are `configured` (in
`mcphub.yaml` already), `available` (installed but not yet added), or
`not_installed`.

### `mode`

Controls what `sync` writes into the agent:

- **`gateway`** (default) — write **only** the `mcphub` gateway, so the agent
  sees a single MCP server and mcphub proxies all the real servers behind it.
  This is the token-saving default: one tool list, one connection.
- **`direct`** — write **every enabled server** straight into the agent,
  verbatim, with no proxy in between.

If `mode` is omitted (or set to anything other than `direct`), mcphub treats
it as `gateway`. See [Concepts](/guide/concepts#gateway-vs-direct).

### `disabled`

Set `disabled: true` to keep an agent's definition in the file but skip it
during `sync`. It's reported as skipped rather than removed, so you can
re-enable it later without retyping `type`/`path`.

### `servers` and `tools`: per-agent routing

By default every enabled server reaches every non-disabled agent. For
multi-agent setups where different agents should see different subsets, give
an agent a `servers` list (which downstream servers it may reach) and/or a
`tools` list (which `server__tool` names it may call — gateway mode only,
since a direct-mode agent talks to each server itself and there's no proxy to
filter individual tools):

```yaml
agents:
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    servers: [codemap, vecgrep]
    tools: [codemap__codemap_find, vecgrep__vecgrep_search]
```

Both fields distinguish **omitted** from **empty**, which matters:

| In `mcphub.yaml` | Meaning |
| --- | --- |
| key omitted entirely | **All.** No restriction — every enabled server / every tool of the allowed servers. |
| `servers: []` / `tools: []` | **None.** A deliberately minimal agent. |
| `servers: [a, b]` / `tools: [a__x]` | **Only these.** |

In **gateway** mode, a scoped agent's harness entry is launched as
`mcphub mcp serve --agent <name>`, so the spawned gateway advertises only that
subset and refuses out-of-scope calls through `mcphub_call_tool`,
`mcphub_describe_tool`, `mcphub_search_tools`, and `mcphub_list_servers`. In
**direct** mode only the listed servers are written into the agent's config
(`tools` doesn't apply there). A listed server that is disabled, or missing
from the enabled set, is dropped silently — routing selects within the
enabled servers, it does not enable anything; naming a server that doesn't
exist anywhere in `servers:` is a validation error. `tools` entries must be
exact `server__tool` names — no wildcards — and when the agent also has a
`servers` list, each tool's server must appear in it. A `tools` list on a
`direct`-mode agent fails validation (`tools routing is gateway-only`).
`mcphub doctor` reports each agent's scope (`routes to N/M enabled servers`)
and flags any listed-but-disabled server.

::: warning Curation, not a security boundary
Routing controls what the gateway *advertises and honors* for a well-behaved
agent — it keeps context lean and agents on-task. It is not a hard isolation
layer: anything that can run `mcphub mcp serve` without `--agent`, or talk to
the downstream servers directly, sees everything. Use it to stop a review
agent from burning tokens on deployment tools, not to protect secrets from a
hostile process.
:::

See [Per-agent routing](/guide/routing) for the full walkthrough.

---

## Full example

```yaml
version: 1

# all (default) mounts every downstream tool as 'server__tool';
# lazy advertises only mcphub's eight meta-tools and serves the rest on demand.
expose: all

servers:
  codemap:
    command: codemap
    args: [serve]
    enabled: true
    description: Code knowledge graph
    tags: [code, search]
    use_when: ["understand symbols and references in a codebase"]
  vecgrep:
    command: vecgrep
    args: [serve, --mcp]
    env:
      VECGREP_HOME: ~/.vecgrep
    enabled: true
    description: Semantic code search
    tags: [code, search]
    use_when: ["find code by meaning when exact names are unknown"]
  bob:
    command: /Users/abdulachik/go/bin/bob
    args: [mcp, serve, --allow-any-workspace]
    enabled: true
    description: Deterministic repository factory and lifecycle reconciler
    tags: [builder, code]
    use_when: ["inspect or plan a repository feature"]
  glyph:
    command: glyph
    args: [mcp]
    enabled: false
    description: TUI behavior testing

  # secrets injected from a tvault project at spawn — never stored in this file
  github:
    command: gh-mcp
    args: [--stdio]
    vault: github            # spawn via `tvault run --project github`
    vault_only: [GH_TOKEN]   # least-privilege allowlist (optional)
    enabled: true
    description: GitHub MCP

  # remote server — a tvault:// header ref resolved at connect time
  obsidian:
    url: "https://127.0.0.1:27124/mcp"
    transport: http
    headers:
      Authorization: "tvault://obsidian/authorization"
    enabled: false
    description: Obsidian Local REST API

# Optional named bundles of servers
groups:
  coding: [bob, codemap, vecgrep]

# Tools that stay mounted directly even under expose: lazy
pin:
  - codemap__codemap_semantic

# Agent harnesses mcphub keeps in sync
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: direct
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    # disabled: true                # skip during sync without deleting the definition
    # Per-agent routing (optional) — restrict what this agent can reach:
    # servers: [codemap, vecgrep]        # only these enabled servers (omit = all; [] = none)
    # tools: [codemap__codemap_find]     # gateway-only: only these server__tool names (omit = all; [] = none)
```

The Bob entry uses `--allow-any-workspace`, which grants its read-only MCP
tools access to any workspace readable by the Bob process. Use that flag only
in a trusted, local, single-user gateway — see the
[Bob integration guide](/guide/bob) for the least-privilege
`--allow-workspace` alternative.

## See also

- [CLI reference](/reference/cli) — every command and flag.
- [Sync to your agents](/guide/sync) — how each harness adapter merges.
- [Per-agent routing](/guide/routing) — `servers`/`tools` scoping in depth.
- [Secrets](/guide/secrets) — `vault` vs. `tvault://` headers, side by side.
- [Lazy mode](/guide/lazy-mode) — `expose: lazy`, pinning, and the eight meta-tools.
- [Concepts](/guide/concepts) — gateway vs. direct, namespacing, token savings.
- [Connect Bob](/guide/bob) — register the repository builder with the right workspace authority.
