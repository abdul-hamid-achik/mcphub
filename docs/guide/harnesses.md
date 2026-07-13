---
title: Supported harnesses
description: "Every agent harness mcphub can sync — all 12, from Claude Code to local-agent — with each one's config path, on-disk format, and the quirks worth knowing."
---

# Supported harnesses

mcphub syncs your `mcphub.yaml` into **twelve agent harnesses**. Each one wants
its MCP servers declared in its own file, in its own format; mcphub's adapters
know all of them, so `mcphub sync` writes the right shape into each — a
non-destructive merge, dry-run by default, with a timestamped `.bak` before any
write. See [Sync](/guide/sync) for the mechanics.

## The twelve

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

The config column is each harness's default path; the `path` field in
`mcphub.yaml` decides what mcphub actually edits, so point it elsewhere if you
keep a config in a non-default location.

## `mcphub agents`

`mcphub agents` lists every harness mcphub can sync to, whether its config file
exists on disk, and whether it is already wired into `mcphub.yaml`:

```sh
mcphub agents          # human-readable table
mcphub agents --json   # machine-readable
```

Each harness is in one of three states:

| State | Meaning |
| --- | --- |
| `configured` | already in `mcphub.yaml` (and the config file exists) |
| `available` | not in `mcphub.yaml`, but the config file exists — add it to sync |
| `not_installed` | neither in `mcphub.yaml` nor on disk — install the tool first |

To adopt an `available` harness, add an entry under `agents:` in `mcphub.yaml`,
or let mcphub do it:

::: tip Import everything at once
`mcphub init --from-agents` scans your installed harness configs, unions every
MCP server they already declare into `mcphub.yaml`, and wires those agents up
in gateway mode — you adopt mcphub without retyping anything.
:::

`mcphub doctor` also reports `available:` entries for harnesses that have a
config file on disk but are not yet in `mcphub.yaml`.

## Declaring agents in `mcphub.yaml`

Each entry under `agents:` names a harness `type`, the config file `path`, and
a sync `mode`:

```yaml
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway            # default — the agent sees only mcphub
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: direct             # every enabled server written verbatim
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    # disabled: true         # skip during sync without deleting the definition
    # servers: [codemap, vecgrep]      # per-agent routing (optional)
    # tools: [codemap__codemap_find]   # gateway-only tool allowlist (optional)
```

Valid types: **`claude`**, **`opencode`**, **`codex`**, **`crush`**,
**`forge`**, **`hermes`**, **`copilot`**, **`qwen`**, **`gemini`**, **`kilo`**,
**`kimi`**. `mode` defaults to `gateway`; an agent marked `disabled: true` is
skipped by sync (and reported as skipped) without losing its definition. The
optional `servers`/`tools` lists scope what an agent can reach — see
[per-agent routing](/reference/config#agents).

`mcphub init` seeds the six original types (`claude`, `opencode`, `codex`,
`crush`, `forge`, `hermes`) into the starter config. The five newer types —
`copilot`, `qwen`, `gemini`, `kilo`, `kimi` — sync exactly like the others but
are not seeded by default: add them by hand or via `mcphub init --from-agents`.

## Per-harness quirks

All twelve adapters share the same guarantees — only the MCP-server section of
the file is touched, every other key is preserved, and pruning is limited to
entries mcphub previously wrote. Within that, the formats differ:

### JSON `mcpServers` — Claude Code, Copilot CLI, Qwen Code, Gemini CLI, Forge

The most common shape: a top-level `mcpServers` object.

- **Claude Code** (`type: claude`) — stdio servers as `command` + `args` +
  `env`; remote servers as a `type` (the transport, default `http`) plus `url`.
  Everything else in `~/.claude.json` (projects, history, UI state) is
  untouched.
- **Copilot CLI** (`type: copilot`) — same shape, but every entry carries an
  explicit `type` (`"local"`/`"stdio"` | `"http"` | `"sse"`). Extra keys like
  `tools`, `headers`, and `timeout` are preserved as unmodeled.
- **Qwen Code** (`type: qwen`) and **Gemini CLI** (`type: gemini`) —
  transport is distinguished by field name instead of a type tag: stdio uses
  `command` + `args`, HTTP uses `httpUrl`, SSE uses `url`. Extra keys
  (`headers`, `timeout`, `trust`, `includeTools`, …) survive as unmodeled.
- **Forge** (`type: forge`) — same `mcpServers` shape as Claude, except each
  entry carries a `disable` boolean rather than a type tag. Forge's own
  convention is a project-local `.mcp.json`; mcphub's default path is the
  home-relative `~/forge/.mcp.json`, so set `path` if you keep it elsewhere.

### JSON `mcp` — opencode, Crush, Kilo Code

- **opencode** (`type: opencode`) — flattens command + args into a single
  `command` **array**, tags entries `type: "local"`/`"remote"`, carries an
  `enabled` flag, and names the env map `environment`.
- **Crush** (`type: crush`) — each entry carries an explicit `type`
  (`"stdio"` | `"http"` | `"sse"`) alongside `command`/`args` or `url`.
- **Kilo Code** (`type: kilo`) — the same entry shape as opencode
  (`type: "local"`/`"remote"`, `command` array, `environment`), but the file is
  **JSONC**. mcphub strips comments before parsing so `.jsonc` reads the same
  as `.json`, but **comments are not preserved on write** — a `.bak` is taken
  first.

### TOML `[mcp_servers.*]` — Codex, Kimi Code CLI

- **Codex** (`type: codex`) — servers live in `[mcp_servers.*]` tables in
  `~/.codex/config.toml`.
- **Kimi Code CLI** (`type: kimi`) — same TOML tables, with opencode-style
  entries (`type: "local"`/`"remote"`, `command` array, `environment`).

::: warning TOML round-trip
Both TOML adapters round-trip the file through a generic map, so **comments and
key ordering in `config.toml` are not preserved** on write. Every value
survives, only the `mcp_servers` subtree is logically changed, a timestamped
`.bak` is always written first, and sync defaults to dry-run — safe, but worth
knowing before your first `--write`.
:::

### YAML — Hermes and local-agent

- **local-agent** (`type: local-agent`) — servers live in a YAML `servers`
  sequence at `~/.config/local-agent/config.yaml`.
- **Hermes** (`type: hermes`) — servers live under a top-level `mcp_servers`
  map in `~/.hermes/config.yaml`; each entry carries an `enabled` flag. Same
  caveat as the TOML adapters: the YAML is round-tripped through a generic map,
  so a write reserializes the whole file — every key's value is preserved, but
  comments and key ordering elsewhere are not.

## Syncing them

```sh
mcphub sync                 # dry run: preview every agent
mcphub sync claude codex    # limit scope to named agents
mcphub sync --write         # apply (a .bak is saved first)
```

In `gateway` mode each harness gets a single `mcphub` entry that runs
`mcphub mcp serve`; in `direct` mode it gets every enabled server verbatim.
After the gateway is in place, `mcphub offload --write` removes the direct
copies of servers mcphub now proxies, which is where the token savings land.

## Next

- [Sync to your agents](/guide/sync) — dry runs, backups, diff semantics, and
  the full adapter walkthrough.
- [Concepts](/guide/concepts) — gateway vs. direct and `server__tool`
  namespacing.
- [Configuration reference](/reference/config) — every `agents:` field.
