# Configuration reference

The mcphub config file is the single source of truth for which downstream MCP
servers exist, how they group, and which agent harnesses mcphub keeps in sync.
Edit this one file (or use [Studio](/guide/studio)), then
[`mcphub sync`](/guide/sync) propagates the result into every agent.

## Format: YAML, TOML, or JSON

The config can be **YAML** (`mcphub.yaml`, the default and the only one that
keeps inline comments), **TOML** (`mcphub.toml`), or **JSON** (`mcphub.json`).
mcphub picks the format from the file extension and reads and writes all three —
so `enable`, `disable`, `add`, and Studio round-trip in whatever format you
chose. Generate one with `mcphub init --format yaml|toml|json`.

## Location

mcphub resolves the config path in this order:

1. the `MCPHUB_CONFIG` environment variable, or the `--config <path>` flag,
2. the first existing `mcphub.{yaml,yml,toml,json}` in the current directory,
3. the first existing one in `~/.config/mcphub/`, else `~/.config/mcphub/mcphub.yaml`.

Generate a starter file with [`mcphub init`](/reference/cli#init).

## Top-level shape

```yaml
version: 1
expose: all   # or: lazy
response_budget: 32KB  # complete serialized MCP result budget; 0 = unlimited
verbatim: false        # true = never spool or replace downstream results
pin:          # tools always mounted, even in lazy mode (optional)
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

| Key       | Type            | Required | Description                                        |
| --------- | --------------- | -------- | -------------------------------------------------- |
| `version` | int             | yes      | Config schema version. Currently `1`.              |
| `expose`  | `all` \| `lazy` | no       | Gateway tool exposure. `all` (default) mounts every downstream tool as `server__tool`; `lazy` advertises only mcphub's meta-tools and serves tools on demand via `mcphub_call_tool`. |
| `pin`     | list of strings | no       | Tools that stay mounted even in `lazy` mode, so agents call them automatically. Each entry is a bare server (`codemap` — all its tools), a wildcard (`codemap__*`), or one tool (`codemap__codemap_semantic`). Manage with `mcphub pin` / `unpin` (or `p` in Studio). |
| `response_budget` | byte size string | no | Complete serialized MCP result budget. Default `32KB`; `0` is unlimited. Oversized results are stored locally for 24 hours and recovered with `mcphub_get_result`. |
| `verbatim` | bool | no | Return every downstream result unchanged and disable result spooling. Default `false`. |
| `servers` | map             | yes      | The downstream MCP servers mcphub manages.         |
| `groups`  | map             | no       | Named bundles of server names.                     |
| `agents`  | map             | yes      | The agent harnesses mcphub keeps in sync.          |

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

  # remote server
  memory:
    url: "https://mcp.example.com/sse"
    transport: sse
    enabled: false
    description: Hosted memory service
```

### Fields

| Field         | Type                | Applies to | Description                                                                 |
| ------------- | ------------------- | ---------- | --------------------------------------------------------------------------- |
| `command`     | string              | stdio      | The executable to spawn for a local stdio server.                           |
| `args`        | list of strings     | stdio      | Arguments passed to `command`.                                              |
| `env`         | map of string→string| stdio      | Environment variables for the spawned process (merged over the inherited environment). |
| `url`         | string              | remote     | The endpoint of a remote server.                                            |
| `transport`   | string              | remote     | `http` (default) or `sse`.                                                  |
| `vault`        | string             | stdio      | A [TinyVault](https://github.com/abdul-hamid-achik/tinyvault) (`tvault`) project. The server is launched via `tvault run --project <vault> -- <command>`, injecting that project's secrets as env vars at spawn — so they never live in `mcphub.yaml`. |
| `vault_only`   | list of strings    | stdio      | Inject only these secret keys (least-privilege allowlist).                  |
| `vault_prefix` | string             | stdio      | Inject only secret keys with this prefix.                                   |
| `enabled`     | bool                | both       | Whether the gateway connects to it and `sync` (direct mode) writes it.      |
| `description` | string              | both       | Human-readable note shown in `list`, Studio, and `mcphub_list_servers`.     |
| `tags`        | list of strings     | both       | Free-form labels.                                                           |

### Rules (validated on load)

- A server must set **either** `command` **or** `url` — not both, and not
  neither.
- `transport`, if set, must be `http` or `sse`. For a remote server with no
  `transport`, mcphub uses streamable HTTP.
- `vault` requires a `command` (it wraps a spawned process) — it can't be used
  with a remote `url`.
- Only **enabled** servers are connected by the gateway and written by
  `sync` in direct mode.

### Secrets via tvault

Rather than putting API keys in `env` (which lands in `mcphub.yaml` in plain
text), point a server at a `tvault` project:

```yaml
servers:
  github:
    command: gh-mcp
    args: [--stdio]
    vault: github            # inject the "github" tvault project's secrets
    vault_only: [GH_TOKEN]   # ...but only this key
    enabled: true
```

In **gateway** mode the hub spawns the server through `tvault run`, so the
secrets reach the downstream process and never touch any agent. In **direct**
mode `sync` writes the `tvault run … -- gh-mcp` wrapper into the agent's config,
so the agent launches it the same way. `mcphub doctor` checks that `tvault` is
on `PATH` whenever a server uses a vault. Add one from the CLI with
`mcphub add github gh-mcp --vault github --vault-only GH_TOKEN`.

### stdio vs. remote

A server with a `command` is a **stdio** server: the gateway spawns it as a
subprocess and speaks MCP over its stdin/stdout, with any `env` merged over the
inherited environment. A server with a `url` is **remote**: the gateway connects
over the network using the chosen `transport` (`http` streamable, or `sse`).

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
group), then run `mcphub sync` to apply.

---

## `agents`

A map of agent name to the harness mcphub syncs into. Each agent points at a
config file and chooses a mode.

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
    disabled: false
```

### Fields

| Field      | Type   | Required | Description                                                                   |
| ---------- | ------ | -------- | ----------------------------------------------------------------------------- |
| `type`     | string | yes      | The harness adapter: `claude`, `opencode`, `codex`, `crush`, `forge`, `hermes`, `copilot`, `qwen`, `gemini`, `kilo`, or `kimi`. |
| `path`     | string | yes      | The harness config file. Supports leading `~` expansion.                      |
| `mode`     | string | no       | `gateway` (default) or `direct`.                                              |
| `disabled` | bool   | no       | Skip this agent during `sync` without deleting its definition.                |

### `type`

Selects the file-format adapter that translates mcphub's view of servers into
the harness's on-disk format:

| `type`     | Target file (typical)                    | MCP section        |
| ---------- | ---------------------------------------- | ------------------ |
| `claude`   | `~/.claude.json`                         | `mcpServers`       |
| `opencode` | `~/.config/opencode/opencode.json`       | `mcp`              |
| `codex`    | `~/.codex/config.toml`                   | `[mcp_servers.*]`  |
| `crush`    | `~/.config/crush/crush.json`             | `mcp`              |
| `forge`    | `.mcp.json`                              | `mcpServers`       |
| `hermes`   | `~/.hermes/config.yaml`                  | `mcp_servers`      |
| `copilot`  | `~/.copilot/mcp-config.json`             | `mcpServers`       |
| `qwen`     | `~/.qwen/settings.json`                  | `mcpServers`       |
| `gemini`   | `~/.gemini/settings.json`                | `mcpServers`       |
| `kilo`     | `~/.config/kilo/kilo.jsonc`              | `mcp`              |
| `kimi`     | `~/.kimi/config.toml`                    | `[mcp_servers.*]`  |

See [Sync to your agents](/guide/sync#the-harness-adapters) for each adapter's
format details (including the note that the Codex and Kimi TOML round-trips do
not preserve comments or key ordering, and that Kilo's JSONC parser strips
comments on write).

### `mode`

Controls what `sync` writes into the agent:

- **`gateway`** (default) — write **only** the `mcphub` gateway, so the agent
  sees a single MCP server and mcphub proxies all the real servers behind it.
  This is the token-saving default: one tool list, one connection.
- **`direct`** — write **every enabled server** straight into the agent,
  verbatim.

If `mode` is omitted (or set to anything other than `direct`), mcphub treats it
as `gateway`. See [Concepts](/guide/concepts#gateway-vs-direct).

### `disabled`

Set `disabled: true` to keep an agent's definition in the file but skip it during
`sync`. It's reported as skipped rather than removed.

---

## Full example

```yaml
version: 1

servers:
  codemap:
    command: codemap
    args: [serve]
    enabled: true
    description: Code knowledge graph
    tags: [code, search]
  vecgrep:
    command: vecgrep
    args: [serve, --mcp]
    enabled: true
    description: Semantic code search
    tags: [code, search]
  glyph:
    command: glyph
    args: [mcp]
    enabled: false
    description: TUI behavior testing

groups:
  coding: [codemap, vecgrep]

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
```

## See also

- [CLI reference](/reference/cli) — every command and flag.
- [Sync to your agents](/guide/sync) — how each harness adapter merges.
- [Concepts](/guide/concepts) — gateway vs. direct, namespacing, token savings.
