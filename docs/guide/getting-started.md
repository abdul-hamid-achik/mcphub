# Getting started

mcphub is a gateway and control plane for [Model Context Protocol](https://modelcontextprotocol.io)
servers. You define your MCP servers **once** in `mcphub.yaml` (or the Studio
TUI), and mcphub does two jobs for you:

- **Runs a single gateway** — `mcphub mcp serve` connects to every enabled
  downstream server, aggregates their tools, and re-exposes them on one stdio
  connection, so an agent connects to one server instead of a dozen.
- **Syncs your agents** — `mcphub sync` writes the right MCP config into every
  supported agent harness (Claude Code, opencode, Codex, Copilot CLI, Qwen
  Code, Gemini CLI, Kilo Code, Kimi Code CLI, Crush, Forge, Hermes) so you
  never hand-edit each one.

Think of it as a *MCP Docker Kit, without Docker*: one place to declare your
servers, one connection to front them all, and one command to push that setup
everywhere.

## Install

mcphub is a single Go binary. Build it from the repository root:

```sh
task build      # writes bin/mcphub
```

Or install it onto your `PATH`:

```sh
task install    # builds a release binary and copies it to the install path
```

You can also use the standard Go toolchain directly:

```sh
go build -o bin/mcphub ./cmd/mcphub
```

Check the version:

```sh
mcphub --version
```

## Create your config

Write a starter `mcphub.yaml`:

```sh
mcphub init
```

This creates a config at the [default path](#where-things-live) with a few
example servers and six agent harnesses pre-wired (claude, opencode, codex,
crush, forge, hermes). Five more (copilot, qwen, gemini, kilo, kimi) are
available and can be added to `agents` in `mcphub.yaml` — run `mcphub agents`
to see every type and its status. Pass `--force` to overwrite an existing
file.

Open it and describe your real servers. A stdio server is a `command` plus
`args`; a remote server is a `url` plus a `transport`:

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

agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
```

See the [configuration reference](/reference/config) for every field.

## See what you have

```sh
mcphub list          # table of configured servers (alias: ls)
mcphub list --json   # machine-readable
```

Toggle servers on and off without editing YAML by hand:

```sh
mcphub enable vecgrep
mcphub disable glyph
```

## Preview, then sync

`sync` is a **dry run by default** — it prints the diff it would apply and
changes nothing:

```sh
mcphub sync
```

When the plan looks right, apply it. mcphub writes a timestamped `.bak` of each
file before touching it:

```sh
mcphub sync --write
```

Read [Sync to your agents](/guide/sync) for how the merge works per harness.

## Point an agent at the gateway

In **gateway** mode (the default), `sync` writes a single `mcphub` server into
the agent. That server is `mcphub mcp serve`, which proxies all your real
servers behind one connection. You generally never run it by hand — the agent
launches it — but you can verify it boots:

```sh
mcphub mcp serve
```

## Inspect usage

Every proxied tool call is recorded locally. Once an agent has used the gateway:

```sh
mcphub stats          # calls, errors, latency, estimated token cost
mcphub stats --json
```

## Browse in the TUI

```sh
mcphub studio         # alias: tui
```

Studio lets you browse servers, toggle them with the spacebar, and watch the
usage stats — then run `mcphub sync` to push the result. See
[Studio](/guide/studio).

## Diagnose

If something looks off, `doctor` checks your config, whether each enabled
server's command is on `PATH`, whether each agent's config file exists, and that
the intelligence store opens:

```sh
mcphub doctor
mcphub doctor --json
```

## Where things live

mcphub reads two paths, each overridable by a flag or an environment variable:

| What            | Default                                  | Env             | Flag       |
| --------------- | ---------------------------------------- | --------------- | ---------- |
| Config          | `./mcphub.yaml` or `~/.config/mcphub/mcphub.yaml` | `MCPHUB_CONFIG` | `--config` |
| Intelligence DB | `~/.local/share/mcphub/mcphub.db`        | `MCPHUB_DB`     | `--db`     |

The config default prefers `$MCPHUB_CONFIG`, then a `mcphub.yaml` in the current
directory, then the XDG config path.

## Next steps

- [Concepts](/guide/concepts) — gateway vs. direct, namespacing, and how the
  proxy saves tokens.
- [Sync to your agents](/guide/sync) — how each harness adapter merges.
- [Connect Bob](/guide/bob) — expose Bob's repository inspection and planning tools safely.
- [CLI reference](/reference/cli) — every command and flag.
- [Configuration reference](/reference/config) — the full `mcphub.yaml` schema.
