---
title: Getting Started
description: Install mcphub with Homebrew, go install, or from source; write mcphub.yaml, preview and apply a sync, point an agent at the gateway, and verify with doctor.
---

# Getting started

mcphub is a gateway and control plane for [Model Context Protocol](https://modelcontextprotocol.io)
servers. You define your servers **once** in `mcphub.yaml` (or the Studio TUI), and mcphub does
two jobs from that single file:

- **Runs a single gateway** — `mcphub mcp serve` connects to every enabled downstream server,
  aggregates their tools under namespaced `server__tool` names, and re-exposes them on one stdio
  connection, so an agent connects to one server instead of a dozen.
- **Syncs your agents** — `mcphub sync` writes the right MCP config into every supported agent
  harness (Claude Code, opencode, Codex, Copilot CLI, Qwen Code, Gemini CLI, Kilo Code, Kimi Code
  CLI, Crush, Forge, Hermes) so you never hand-edit each one.

Think of it as *MCP Docker Kit, without Docker*: one place to declare your servers, one
connection to front them all, one command to push that setup everywhere.

This page takes you from zero to a synced agent. It ends with `mcphub doctor` telling you
everything is healthy.

## Install

::: code-group

```sh [Homebrew]
brew install abdul-hamid-achik/tap/mcphub
```

```sh [go install]
go install github.com/abdul-hamid-achik/mcphub/cmd/mcphub@latest
```

```sh [Build from source]
git clone https://github.com/abdul-hamid-achik/mcphub
cd mcphub
task install    # release build, installed to /opt/homebrew/bin
```

:::

Prebuilt archives for every release are also published on the
[GitHub Releases page](https://github.com/abdul-hamid-achik/mcphub/releases) — download the one
for your OS/arch and put the binary on your `PATH`. Releases are built with
[GoReleaser](https://goreleaser.com) and the same tags publish to the Homebrew tap.

If you're hacking on mcphub itself instead of just installing it, `task build` writes
`bin/mcphub` without touching your `PATH`, and `go build -o bin/mcphub ./cmd/mcphub` works with
just the Go toolchain (1.25+).

Confirm it's on your `PATH`:

```sh
mcphub --version
```

## Write a starter config

```sh
mcphub init
```

This writes a starter `mcphub.yaml` to the [default config path](#where-things-live) with a
couple of example servers and agent harnesses pre-wired. Pass `--force` to overwrite an existing
file, or `--format toml` / `--format json` if you'd rather not use YAML — mcphub reads and writes
all three.

::: tip Already using MCP servers in your agents?
Run `mcphub init --from-agents` instead. It scans your installed harness configs (Claude Code,
opencode, Codex, Crush, Forge, Hermes), unions every MCP server they already declare, and wires
those agents up in gateway mode — so you adopt mcphub without retyping servers you've already
configured by hand.
:::

```sh
mcphub init --from-agents
```

Run `mcphub agents` any time to see every harness type mcphub supports and whether it's
`configured` (in `mcphub.yaml`), `available` (installed but not yet wired up), or
`not_installed`.

## Edit mcphub.yaml

Open the config and describe your real servers. A stdio server is a `command` plus `args`; a
remote server is a `url` plus a `transport`:

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

agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
```

You don't have to hand-edit at all — `mcphub add` registers a server from the CLI:

```sh
mcphub add codemap codemap serve            # stdio server
mcphub add ctx7 --url https://mcp.ctx7.io   # remote (http) server
```

Each agent has a `mode`: **`gateway`** (the default) writes only the `mcphub` server into the
agent, which then proxies everything else — this is what saves tokens. **`direct`** writes every
enabled server straight into that agent instead. See the
[full configuration reference](/reference/config) for every field, including secrets via
`vault:`, per-agent `servers`/`tools` routing, and `expose: lazy`.

## See what you have

```sh
mcphub list          # table of configured servers (alias: ls)
mcphub list --json   # machine-readable
```

Toggle servers on and off without hand-editing YAML:

```sh
mcphub enable vecgrep
mcphub disable glyph
```

## Preview the sync, then apply it

`mcphub sync` reconciles every enabled agent harness with `mcphub.yaml`. It is **dry-run by
default** — it prints the exact diff it would apply and changes nothing:

```sh
mcphub sync
```

Name one or more agents to limit the scope:

```sh
mcphub sync claude
```

When the plan looks right, apply it. mcphub writes a timestamped `.bak` of each file before
touching it, and only ever touches the MCP-server section it owns — anything else in the file,
including servers you added by hand, is left alone:

```sh
mcphub sync --write
```

::: warning
`sync` mutates real agent config files once you pass `--write`. Read the dry-run diff first —
that's the whole reason it defaults to a preview.
:::

Read [Sync to your agents](/guide/sync) for how the merge works per harness.

## Point your first agent at the gateway

In **gateway** mode (the default), the `sync --write` you just ran already wrote a single
`mcphub` server into the agent — there is nothing else to wire up by hand. That entry runs
`mcphub mcp serve`, the stdio MCP server that proxies all your real servers behind one
connection. The agent launches it for you; you don't run it directly in normal use. To confirm it
starts cleanly, you can still run it by hand and stop it once you see it hasn't errored:

```sh
mcphub mcp serve
```

Restart (or open) your agent and it will pick up the new `mcphub` server on its next launch.

## Verify with doctor

```sh
mcphub doctor
```

`doctor` checks that your config parses, every enabled server's command is on `PATH`, each
agent's config target exists, and the intelligence store opens. Pass `--probe` to go further —
it actually spawns each enabled server, performs the MCP handshake, and reports how many tools
each one exposes (or why it failed):

```sh
mcphub doctor --probe
```

Once your agent has made a few calls through the gateway, `mcphub stats` shows what it's actually
using:

```sh
mcphub stats
```

## Where things live

mcphub reads two paths, each overridable by a flag or an environment variable:

| What            | Default                                           | Env             | Flag       |
| --------------- | -------------------------------------------------- | --------------- | ---------- |
| Config          | `./mcphub.yaml` or `~/.config/mcphub/mcphub.yaml` | `MCPHUB_CONFIG` | `--config` |
| Intelligence DB | `~/.local/share/mcphub/mcphub.db`                 | `MCPHUB_DB`     | `--db`     |

The config path prefers `--config`, then `$MCPHUB_CONFIG`, then a `mcphub.yaml` in the current
directory, then the XDG config path.

## Next steps

- [Concepts](/guide/concepts) — gateway vs. direct, namespacing, and how the proxy saves tokens.
- [Sync to your agents](/guide/sync) — how each harness adapter merges, and how to undo a sync.
- [Supported harnesses](/guide/harnesses) — the config file and format each agent uses.
- [Studio](/guide/studio) — the interactive TUI for toggling servers and watching usage.
- [Local intelligence](/guide/intelligence) — what `mcphub stats` and `mcphub status` tell you.
- [Connect Bob](/guide/bob) — expose Bob's repository inspection and planning tools safely.
- [CLI reference](/reference/cli) — every command and flag.
- [Configuration reference](/reference/config) — the full `mcphub.yaml` schema.
