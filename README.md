# mcphub

[![CI](https://github.com/abdul-hamid-achik/mcphub/actions/workflows/ci.yml/badge.svg)](https://github.com/abdul-hamid-achik/mcphub/actions/workflows/ci.yml)

**One hub for all your MCP servers, synced into every agent.** MCP Docker Kit, without Docker.

Define your Model Context Protocol servers **once** in `mcphub.yaml` (or the Studio TUI).
Then `mcphub` runs a single gateway that proxies them all, keeps every agent harness in
sync so you never hand-edit a config again, and records each proxied tool call locally so
you can see which servers actually earn their place in your context window.

## Why

Every coding agent — Claude Code, opencode, Codex, Copilot CLI, Qwen Code, Gemini CLI,
Kilo Code, Kimi Code CLI, Crush, Forge, Hermes, local-agent — wants its MCP servers
configured in its own bespoke file, in its own format, by hand. Add a server and you
edit twelve configs. Each agent then loads the full tool list of every server you gave
it, burning context on tools it will rarely call.

mcphub fixes both halves:

- **Stop hand-editing.** One `mcphub.yaml` is the source of truth. `mcphub sync` writes the
  correct config into each agent for you — non-destructively, with a backup, dry-run first.
- **One gateway = fewer tools loaded = token savings.** In gateway mode an agent connects to a
  single `mcphub` server. mcphub fans out to every downstream server behind it and re-exposes
  their tools under namespaced `server__tool` names on one stdio connection. The agent sees
  one server instead of a dozen.

## Features

- **Single gateway** — `mcphub mcp serve` connects to every enabled downstream server as an
  MCP client, aggregates their tools under `server__tool` names, and re-exposes them on one
  stdio connection.
- **Syncs to twelve harnesses** — push your config into Claude Code, opencode, Codex,
  Copilot CLI, Qwen Code, Gemini CLI, Kilo Code, Kimi Code CLI, Crush, Forge, Hermes,
  and local-agent with a non-destructive merge. Dry-run by default; `--write` applies
  after saving a timestamped `.bak`. `mcphub init --from-agents` imports what you
  already have.
- **Two sync modes** — `gateway` (the agent sees only mcphub) or `direct` (every enabled server
  written verbatim), chosen per agent.
- **Lazy exposure + pinning** — `expose: lazy` advertises only eight management tools and serves
  the rest on demand (huge token savings); context-aware routing uses each server's `use_when`
  hints to find unpinned tools, while `pin: [server__tool]` keeps hot tools mounted.
- **Bounded, lossless results** — oversized MCP responses are stored locally for 24 hours and
  replaced with a compact `callId`; agents recover the exact serialized result in bounded pages
  with `mcphub_get_result`. Small results pass through unchanged, and `verbatim: true` or
  `response_budget: "0"` opts out.
- **Long-running calls** — `mcphub_call_tool` accepts `detach: true` for downstream tools that
  would outlive the client's tool-call timeout (repo indexing, big scans): the call keeps running
  in the background and returns a `callId` immediately, collected later with `mcphub_poll_result`.
  An optional `timeout_ms` bounds any call, clamped by the `call_timeout` config (default 30m).
- **Local intelligence** — every proxied call is recorded to SQLite, so `mcphub stats`
  (`--tools`, `--recent`, `--since 7d`) and `mcphub status` (per-agent **sync drift** + flags
  enabled-but-unused servers) tell you which servers earn their context budget.
- **Config in YAML, TOML, or JSON** — pick with `mcphub init --format`; mcphub reads and writes
  all three.
- **Markdown reports** — `mcphub status --markdown` / `mcphub stats --markdown` for pasting into
  notes or issues (`--json` too, for scripting).
- **Secrets via [tvault](https://github.com/abdul-hamid-achik/tinyvault)** — `vault: <project>`
  injects a server's secrets at spawn through `tvault run`, so they never live in the config.
- **Studio TUI** — bubbletea v2 + harmonica: three tabs (Servers / Agents / Stats), toggle with
  space, preview-and-apply a sync with `s`, flip exposure with `x`.
- **Doctor** — `mcphub doctor` diagnoses config, server availability, agent targets, and the
  store; `--probe` actually connects to each server for a real health check.
- **stdio and remote servers** — local `command`/`args` servers or remote `http`/`sse` URLs.
- **Pure Go, no cgo** — the SQLite store uses `modernc.org/sqlite`; the binary is self-contained.

## Install

```sh
brew install abdul-hamid-achik/tap/mcphub          # macOS / Linux (Homebrew)
go install github.com/abdul-hamid-achik/mcphub/cmd/mcphub@latest
# or download a prebuilt archive from the Releases page, or build from source (below)
```

## Quick start

```sh
# 1. Build from source (Go 1.25+) — pick one
task install                       # build with version metadata, install to /opt/homebrew/bin
go build -o bin/mcphub ./cmd/mcphub

# 2. Write a starter config (YAML by default; --format toml or json also work)
mcphub init                        # creates ~/.config/mcphub/mcphub.yaml
mcphub init --from-agents          # ...or import the servers your agents already declare

# 3. Edit mcphub.yaml — add your servers and agents, toggle what's enabled
mcphub list                        # see what's configured

# 4. Preview what sync would write (dry-run, changes nothing)
mcphub sync                        # add --write to actually apply

# 5. Point an agent at the gateway and use it
mcphub mcp serve                   # the single stdio MCP server that proxies the rest
```

In gateway mode, step 4 writes a single `mcphub` server into each agent that runs
`mcphub mcp serve` for you — there is nothing else to wire up.

## Concepts

### Gateway vs direct mode

Each agent declares a `mode` in `mcphub.yaml`:

- **`gateway`** (default) — mcphub writes **only** the `mcphub` server into the agent. The agent
  sees one MCP server; mcphub proxies all the rest behind it. Fewer tools are loaded into the
  agent's context, which **saves tokens**.
- **`direct`** — mcphub writes **every enabled server** straight into the agent, verbatim. No
  proxy; the agent talks to each server itself.

### Namespaced `server__tool`

The gateway connects to each downstream server and exposes its tools under a namespaced name:
a `search` tool on a server named `vecgrep` becomes `vecgrep__search`. Names never collide, and
you always know which server a tool came from.

### Local intelligence

Every call the gateway proxies is recorded to a local SQLite database
(`~/.local/share/mcphub/mcphub.db`). `mcphub stats` turns that into a per-server and per-tool
report — calls, errors, average latency, and estimated token cost — so you can tell which
servers earn their context budget and which to disable.

## Command reference

| Command | What it does |
| --- | --- |
| `mcphub init [--force] [--from-agents]` | Write a starter `mcphub.yaml`, or `--from-agents` to import servers your harnesses already declare. |
| `mcphub list` \| `ls` | List configured servers (state, kind, target, tags, description). |
| `mcphub add <name> [cmd args...]` \| `--url` | Register a server (stdio command or remote `--url`; `--vault` for tvault secrets). |
| `mcphub remove <server>` \| `rm` | Offload a server from `mcphub.yaml`. |
| `mcphub enable <server>` | Enable a server in `mcphub.yaml`. |
| `mcphub disable <server>` | Disable a server in `mcphub.yaml`. |
| `mcphub groups` | List server groups. |
| `mcphub use <group> [--only]` | Enable every server in a group (`--only` disables the rest). |
| `mcphub pin <server\|tool...>` \| `--top N` | Keep tools directly callable in lazy mode (whole server, `srv__*`, or one tool; `--top N` auto-pins your most-used). |
| `mcphub unpin <server\|tool...>` | Remove pins. |
| `mcphub status` | Config, per-agent sync drift, and usage intelligence at a glance. |
| `mcphub sync [agent...] [--write]` | Reconcile agent harnesses with the config. Dry-run unless `--write`. |
| `mcphub offload [agent...] [--write]` | Remove gateway-proxied servers from agents, leaving just `mcphub`. Dry-run unless `--write`. |
| `mcphub studio` \| `tui` | Launch the interactive Studio TUI. |
| `mcphub stats [--tools] [--recent N]` | Show local tool-call intelligence. |
| `mcphub doctor [--probe]` | Diagnose config, servers, agents, and the store (`--probe` connects for real). |
| `mcphub mcp serve` | Run the gateway MCP stdio server that proxies all enabled servers. |
| `mcphub agents` | List all supported agent harnesses and their status (configured / available / not installed). |

**Persistent flags:** `--config <path>`, `--db <path>`, `--json`, `--version`.

**Environment:** `MCPHUB_CONFIG` and `MCPHUB_DB` override the default paths
(`~/.config/mcphub/mcphub.yaml`, `~/.local/share/mcphub/mcphub.db`). The config is also picked
up from `./mcphub.yaml` if present.

## Configuration

A full `mcphub.yaml`:

```yaml
version: 1

# How the gateway advertises tools to agents:
#   all  (default) — mount every downstream tool as 'server__tool'
#   lazy           — advertise only mcphub's meta-tools; agents resolve or search
#                    capabilities and invoke through mcphub_call_tool (saves tokens)
expose: all

servers:
  # stdio servers — a local command mcphub launches and speaks MCP to
  codemap:
    command: codemap
    args: [serve]
    enabled: true
    description: Code knowledge graph
    tags: [code, search]
    use_when: ["understand symbols, references, and structure in a codebase"]
  vecgrep:
    command: vecgrep
    args: [serve, --mcp]
    env:
      VECGREP_HOME: ~/.vecgrep
    enabled: true
    description: Semantic code search
    tags: [code, search]
    use_when: ["find code by meaning when exact symbol names are unknown"]
  bob:
    command: /Users/abdulachik/go/bin/bob
    args: [mcp, serve, --allow-any-workspace]
    enabled: true
    description: Deterministic repository factory and lifecycle reconciler
    tags: [builder, code]
    use_when: ["inspect or plan a repository feature before implementation"]
  glyph:
    command: glyph
    args: [mcp]
    enabled: false
    description: TUI behavior testing

  # secrets injected from a tvault project at spawn — never stored in this file
  github:
    command: gh-mcp
    args: [--stdio]
    vault: github          # spawn via `tvault run --project github`
    vault_only: [GH_TOKEN]  # least-privilege allowlist (optional)
    enabled: true
    description: GitHub MCP

  # remote servers — an http or sse URL
  beta:
    url: "https://example.com/mcp"
    transport: http        # http | sse
    enabled: false

# Optional named bundles of servers
groups:
  coding: [codemap, vecgrep]

# Agent harnesses mcphub keeps in sync
#   mode: gateway -> the agent sees ONLY mcphub, which proxies the rest (saves tokens)
#   mode: direct  -> every enabled server is written straight into the agent
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
    # disabled: true       # skip during sync without deleting the definition
    # Per-agent routing (optional) — restrict what this agent can reach:
    # servers: [codemap, vecgrep]   # only these enabled servers (omit = all; [] = none)
    # tools: [codemap__codemap_find]  # gateway-only: only these server__tool names (omit = all; [] = none)
  local-agent:
    type: local-agent
    path: ~/.config/local-agent/config.yaml
    mode: gateway
```

Each `server` is either a stdio server (`command` + `args` + optional `env`) **or** a remote
server (`url` + `transport`, where `transport` is `http` or `sse`). Each `agent` has a `type`
(`claude`, `opencode`, `codex`, `crush`, `forge`, `hermes`, `copilot`, `qwen`, `gemini`,
`kilo`, `kimi`, or `local-agent`), a `path`, and a `mode` that defaults to `gateway`.

The Bob entry uses `--allow-any-workspace`, which grants its read-only MCP tools
access to any workspace readable by the Bob process. Use that flag only in a
trusted, local, single-user gateway. For a least-privilege setup and the six
available Bob tools, see the [Bob integration guide](docs/guide/bob.md).

### Per-agent routing

Every enabled server normally reaches every non-disabled agent. For multi-agent setups where
different agents should see different subsets, give an agent a `servers` list (which downstream
servers it may reach) and/or a `tools` list (which `server__tool` names it may call — gateway mode
only, since direct agents talk to servers themselves):

```yaml
agents:
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    servers: [codemap, vecgrep]
    tools: [codemap__codemap_find, vecgrep__vecgrep_search]
```

In gateway mode a scoped agent's harness entry is launched as `mcphub mcp serve --agent <name>`,
so excluded servers are not started or contacted, the gateway advertises only that subset, and
out-of-scope calls are refused. In direct mode only the listed servers are written. An agent with no `servers`/`tools` (omitted) sees everything,
as before; an explicit empty list (`servers: []` / `tools: []`) means **none** — a deliberately
minimal agent. This is least activation and curation, not an OS security boundary.

Set top-level `expose: lazy` to have the gateway advertise only its meta-tools (saving tokens —
agents route current task context with `mcphub_resolve_tool`, browse with
`mcphub_search_tools`, and run a tool with `mcphub_call_tool`). Add concise
`use_when` phrases to each server so the resolver can connect user intent to narrowly named tools.
Harness authors can follow the [contextual routing contract](https://mcphubcli.dev/guide/contextual-routing)
to trigger discovery at task and phase changes without hardcoding server mappings. Use
`vault: <project>` on a server (optionally narrowed with `vault_only` / `vault_prefix`) to inject
a [TinyVault](https://github.com/abdul-hamid-achik/tinyvault) project's secrets at spawn instead
of writing them into `mcphub.yaml`.

## How sync works

`mcphub sync` reconciles each agent's config with `mcphub.yaml`:

- **Dry-run by default.** It prints the exact diff it would apply and changes nothing. Pass
  `--write` to actually edit the files.
- **Non-destructive merge.** mcphub only touches the server entries it owns. Anything else in the
  agent's config — your settings, other servers you added by hand — is left untouched.
- **Backup first.** Before writing, mcphub saves a timestamped `.bak` of the file.
- **Scoped.** Name one or more agents to limit the run (`mcphub sync claude codex`); with no
  names, all enabled agents are synced. Agents marked `disabled` are skipped.

```sh
mcphub sync               # preview every agent
mcphub sync claude        # preview just one
mcphub sync --write       # apply (a .bak is saved first)
```

## Supported harnesses

| Harness | Config | Format |
| --- | --- | --- |
| Claude Code | `~/.claude.json` | JSON `mcpServers` |
| opencode | `~/.config/opencode/opencode.json` | JSON `mcp` |
| Codex | `~/.codex/config.toml` | TOML `[mcp_servers.*]` |
| Copilot CLI | `~/.copilot/mcp-config.json` | JSON `mcpServers` |
| Qwen Code | `~/.qwen/settings.json` | JSON `mcpServers` |
| Gemini CLI | `~/.gemini/settings.json` | JSON `mcpServers` |
| Kilo Code | `~/.config/kilo/kilo.jsonc` | JSONC `mcp` |
| Kimi Code CLI | `~/.kimi/config.toml` | TOML `[mcp_servers.*]` |
| Crush | `~/.config/crush/crush.json` | JSON `mcp` |
| Forge (forgecode) | `~/forge/.mcp.json` | JSON `mcpServers` |
| Hermes | `~/.hermes/config.yaml` | YAML `mcp_servers` |
| local-agent | `~/.config/local-agent/config.yaml` | YAML `servers` sequence |

## Studio TUI

`mcphub studio` (alias `tui`) is an interactive terminal UI built on bubbletea v2. Browse your
servers, toggle them on and off with **space**, and inspect local usage intelligence — then run
`mcphub sync` to push the result to every agent.

```sh
mcphub studio
task run        # build, then launch Studio
```

## Development

mcphub is a standard Go module (`github.com/abdul-hamid-achik/mcphub`, Go 1.25). Common tasks
live in `Taskfile.yml`:

| Task | Description |
| --- | --- |
| `task build` | Build `bin/mcphub` with version metadata. |
| `task run` | Build, then launch the Studio TUI. |
| `task serve` | Build, then run the gateway on stdio. |
| `task sync` | Build, then dry-run a sync of all agents. |
| `task test` | Run all unit tests (`go test ./...`). |
| `task cover` | Tests with an HTML coverage report. |
| `task vet` / `task fmt` / `task tidy` | `go vet`, `gofmt -w .`, `go mod tidy`. |
| `task sqlc` | Regenerate the type-safe DB layer from SQL (requires `sqlc`). |
| `task specs` | Run the glyphrun end-to-end TUI/CLI specs. |
| `task docs` / `task docs-build` | Serve / build the VitePress docs site. |
| `task install` | Build an optimized release binary and install it. |

The SQLite store is generated with [sqlc](https://sqlc.dev) from `internal/store/queries` and
`internal/store/migrations`. End-to-end TUI and CLI behavior is exercised with
[glyphrun](https://github.com/abdul-hamid-achik/glyphrun) specs under `specs/` (config in
`glyphrun.config.yml`).

The website + docs live at **[mcphubcli.dev](https://mcphubcli.dev)** — a
self-contained VitePress project under `docs/`, auto-deployed by Vercel on every
push to `main` that touches `docs/` (`docs/vercel.json` skips builds otherwise). Releases are built with [GoReleaser](https://goreleaser.com)
(`.goreleaser.yaml`) and published to the Homebrew tap on a `v*` tag.

```sh
task build && task test
```

## License

MIT © 2026 Abdul Hamid Achik. See [LICENSE](LICENSE).
