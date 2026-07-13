---
title: CLI reference
description: "Every mcphub command and flag, from the real --help output: init, add, sync, offload, pin, stats, doctor, mcp serve, persistent flags, and env vars."
---

# CLI reference

Every mcphub command, with its exact flags. Run `mcphub <command> --help` for
the same information at the terminal.

```sh
mcphub [command] [flags]
```

## Persistent flags

`--config`, `--db`, and `--json` are true persistent flags — set them on any
subcommand:

| Flag              | Description |
| ----------------- | ----------- |
| `--config <path>` | Path to `mcphub.yaml`. Default: `./mcphub.yaml` or `~/.config/mcphub/mcphub.yaml`. |
| `--db <path>`     | Path to the intelligence SQLite db. Default: `~/.local/share/mcphub/mcphub.db`. |
| `--json`          | Emit machine-readable JSON where supported. |
| `-h`, `--help`    | Help for the command. |

::: tip `--version` is root-only
`-v`/`--version` prints the mcphub version and exits, but it only works on the
root command: `mcphub --version` or `mcphub -v`. It is **not** inherited by
subcommands — `mcphub sync --version` fails with `unknown flag: --version`.
:::

### Environment variables

| Variable        | Overrides |
| --------------- | --------- |
| `MCPHUB_CONFIG` | the config path |
| `MCPHUB_DB`     | the intelligence db path |

The config path resolves in order: `--config`, then `$MCPHUB_CONFIG`, then a
`mcphub.yaml` in the current directory, then `~/.config/mcphub/mcphub.yaml`.
The db path resolves: `--db`, then `$MCPHUB_DB`, then
`~/.local/share/mcphub/mcphub.db`.

### Exit status

mcphub exits `0` on success and non-zero on any error (the error is printed to
stderr). [`doctor`](#doctor) exits non-zero when any check fails, which makes it
usable in scripts and CI.

## Commands at a glance

| Command                     | What it does |
| --------------------------- | ------------ |
| [`init`](#init)             | Write a starter config (yaml, toml, or json); `--from-agents` imports what you have. |
| [`list`](#list) (`ls`)      | List configured servers. |
| [`add`](#add)               | Register a server (`add <name> [cmd args...]` or `--url`). |
| [`remove`](#remove) (`rm`)  | Remove a server from `mcphub.yaml`. |
| [`enable`](#enable)         | Enable a server in `mcphub.yaml`. |
| [`disable`](#disable)       | Disable a server in `mcphub.yaml`. |
| [`groups`](#groups)         | List server groups. |
| [`use`](#use)               | Enable every server in a group (`--only` disables the rest). |
| [`pin`](#pin)               | Keep tools directly callable even in lazy mode (`--top N` auto-pins most-used). |
| [`unpin`](#unpin)           | Remove pins. |
| [`status`](#status)         | Config, per-agent sync drift, and usage intelligence at a glance. |
| [`sync`](#sync)             | Write server config into agent harnesses (dry-run by default). |
| [`offload`](#offload)       | Remove gateway-proxied servers from agents, leaving just the mcphub gateway. |
| [`studio`](#studio) (`tui`) | Launch the interactive TUI. |
| [`stats`](#stats)           | Show local tool-call intelligence. |
| [`doctor`](#doctor)         | Diagnose config, server availability, and agent targets. |
| [`agents`](#agents)         | List supported agent harnesses and their status. |
| [`mcp serve`](#mcp-serve)   | Run mcphub as an MCP server (the gateway). |
| `completion`                | Generate a shell autocompletion script. |
| `help`                      | Help about any command. |

---

## `init`

Write a starter config. The config can be YAML (default), TOML, or JSON — pick
with `--format`; mcphub reads and writes all three.

By default it writes a small starter config. With `--from-agents` it scans your
installed harness configs (Claude Code, opencode, Codex, Crush, Forge, Hermes),
unions every MCP server they already declare, and wires those agents up in
gateway mode — so you can adopt mcphub without retyping what you already have.

```sh
mcphub init                        # starter mcphub.yaml
mcphub init --format toml          # ...or mcphub.toml
mcphub init --from-agents          # import servers your agents already declare
mcphub init --force                # overwrite an existing config
```

| Flag              | Description |
| ----------------- | ----------- |
| `--force`         | Overwrite an existing config. |
| `--format <f>`    | Config format: `yaml` (default), `toml`, or `json`. |
| `--from-agents`   | Import servers from your installed harness configs. |

Without `--force`, mcphub refuses to overwrite an existing file. The file is
written to the resolved [config path](#persistent-flags).

---

## `list`

List the servers configured in `mcphub.yaml`. Alias: `ls`.

```sh
mcphub list
mcphub ls --json
```

The table shows each server's `SERVER`, `STATE` (`on`/`off`), `KIND`
(`stdio`/`remote`), `TARGET` (the command or url), and `DESCRIPTION`. With
`--json`, the raw server map is printed instead.

---

## `add`

Register a server in `mcphub.yaml`. For a local stdio server pass a command and
its args; for a remote one pass `--url` instead.

```sh
mcphub add codemap codemap serve            # stdio server
mcphub add ctx7 --url https://mcp.ctx7.io   # remote (http) server
mcphub add db pg-mcp --env DSN=postgres://… --tag data
mcphub add gh gh-mcp --vault github         # secrets injected via tvault
```

| Flag | Description |
| --- | --- |
| `--url <u>` | Remote server URL (instead of a command). |
| `--transport http\|sse` | Remote transport (default `http`). |
| `--description <d>` | Human description. |
| `--env K=V` | Environment variable (repeatable). |
| `--tag <t>` | Tag (repeatable). |
| `--vault <p>` | tvault project to inject secrets from at spawn. |
| `--vault-only <k>` | Inject only these secret keys (repeatable). |
| `--enabled` | Add the server enabled (the default; accepted for compatibility with ecosystem docs). |
| `--disabled` | Add but leave the server disabled. |
| `--force` | Overwrite an existing server. |

Servers are added **enabled** by default. `--enabled` is a no-op alias for the
default, accepted so onboarding commands like `mcphub add <name> <cmd> --enabled`
— common across ecosystem docs — work instead of erroring. `--enabled` and
`--disabled` are mutually exclusive.

---

## `remove`

Remove a server's definition from `mcphub.yaml`. Alias: `rm`.

```sh
mcphub remove <name>
mcphub rm <name>
```

This edits only `mcphub.yaml`. Run [`mcphub sync`](#sync) afterwards to
reconcile your agents with the change. If you only want a server out of the way
temporarily, [`disable`](#disable) keeps its definition around.

---

## `enable`

Enable a server in `mcphub.yaml`. Takes exactly one server name.

```sh
mcphub enable <server>
```

This edits only the config — it does not touch your agents. Run
[`mcphub sync`](#sync) afterwards to apply the change to your harnesses. An
unknown server name is rejected with a pointer to `mcphub list`.

---

## `disable`

Disable a server in `mcphub.yaml`. Takes exactly one server name.

```sh
mcphub disable <server>
```

Same semantics as [`enable`](#enable): config-only, followed by a
[`sync`](#sync) to propagate. [`status`](#status) flags enabled-but-never-called
servers as candidates for `disable`.

---

## `groups`

List the server groups defined under `groups:` in `mcphub.yaml`.

```sh
mcphub groups
mcphub groups --json
```

Groups are named bundles of servers (e.g. `coding: [codemap, vecgrep]`) that
[`use`](#use) can flip on in one command. See the
[configuration reference](/reference/config) for the schema.

---

## `use`

Enable every server in a group.

```sh
mcphub use coding           # enable every server in the 'coding' group
mcphub use coding --only    # ...and disable every server NOT in the group
```

| Flag     | Description |
| -------- | ----------- |
| `--only` | Also disable every server not in the group. |

Like `enable`/`disable`, this edits only `mcphub.yaml` — run
[`mcphub sync`](#sync) to push the new set to your agents.

---

## `pin`

Keep tools mounted directly on the gateway even under `expose: lazy`, so your
agents call them automatically instead of going through `mcphub_search_tools`
first. A pin can be a whole server (pins all its tools), a wildcard, or a single
tool:

```sh
mcphub pin codemap vecgrep              # whole servers
mcphub pin codemap__*                   # same, explicit wildcard
mcphub pin codemap__codemap_semantic    # one tool
mcphub pin --top 8                      # auto-pin your 8 most-called tools (from stats)
mcphub pin                              # list current pins
```

| Flag        | Description |
| ----------- | ----------- |
| `--top <N>` | Auto-pin the N most-called tools from the intelligence store. |

::: tip No sync needed
In gateway mode a pin change takes effect the next time the gateway starts —
there is nothing to sync. Restart your agents to pick it up.
:::

---

## `unpin`

Remove pins. Takes the same server / `server__tool` names as [`pin`](#pin).

```sh
mcphub unpin codemap
mcphub unpin codemap__codemap_semantic
```

---

## `status`

Answer "is everything consistent?" in one screen. For each agent, `status` does
a read-only dry run and reports whether its on-disk MCP config already matches
`mcphub.yaml` (`in sync`) or has changes pending. It also summarizes recorded
usage and flags **enabled servers that have never been called** — candidates to
disable so your agents carry less context.

```sh
mcphub status
mcphub status --markdown        # a report you can paste into notes or an issue
mcphub status --json
mcphub status --server cortex   # scope to one server: routing + proxied calls
```

| Flag | Description |
| --- | --- |
| `--markdown` | Render the report as Markdown (great for notes/issues). |
| `--server <name>` | Scope to one server: which agents route to it + proxied-call count. |

```
Config:  ~/.config/mcphub/mcphub.yaml
Servers: 8 (6 enabled)   Exposure: lazy

AGENT     TYPE      MODE     SYNC
claude    claude    gateway  in sync
codex     codex     gateway  1 pending
opencode  opencode  direct   in sync

Usage:   142 calls, 3 errors, ~38500 est. tokens
Unused:  monitor, vidtrace (enabled but never called)
         → consider `mcphub disable <name>` to shrink agent context.
```

### `--server`

`--server <name>` scopes `status` to one server — a cheap "am I wired into the
gateway?" answer in a single call. Instead of fetching and joining the full
`list`, `doctor`, and `status` inventories, it returns just that server's
registration, enabled state, PATH availability, the agents that route to it,
and how many calls the gateway has proxied to it (`proxied_calls`). With
`--json` the scoped object is the same shape [`doctor --server`](#server)
emits (without the probe fields).

---

## `sync`

Reconcile every agent harness with `mcphub.yaml`. **Dry run by default** — it
prints the diff it would apply and changes nothing. Pass `--write` to actually
edit the files (a timestamped `.bak` is written first). Name one or more agents
to limit the scope; with no names, all enabled agents are synced.

```sh
mcphub sync                  # dry run, all agents
mcphub sync --write          # apply
mcphub sync claude codex     # limit scope to named agents
```

| Argument / flag | Description |
| --------------- | ----------- |
| `[agent...]`    | One or more agent names to sync. With none, all enabled agents are synced. |
| `--write`       | Actually edit the agent config files (a `.bak` is saved first). |
| `--resume <planId>` | Re-sync the agent named in a plan ID (e.g. `plan_1234567890_claude`). |
| `--rollback <planId>` | Restore the exact backup recorded for that plan (falls back to the agent's newest backup, with a note, if the plan was never recorded). |

In **gateway** mode an agent is given a single `mcphub` server that proxies the
rest. In **direct** mode every enabled server is written into the agent. Agents
marked `disabled: true` are skipped. See [Sync to your agents](/guide/sync) for
how each harness adapter merges.

::: tip Safety guarantees
`sync` only ever touches the MCP-server section of each agent's file and
preserves every other key verbatim. Pruning is scoped to entries mcphub
previously *owned* (tracked in the intelligence store), so servers you added by
hand are never clobbered. Every write is preceded by a timestamped `.bak`,
recorded against the printed plan ID — which is what `--rollback` restores.
:::

---

## `offload`

The second half of "register and offload": remove the direct copies of the
servers mcphub now proxies from each gateway-mode agent, so the agent relies
purely on the single `mcphub` gateway. This is where the token savings land —
each agent stops carrying every server's full tool list.

```sh
mcphub offload          # dry-run: show what would be removed from each agent
mcphub offload --write  # apply (a .bak is saved per file first)
```

| Argument / flag | Description |
| --------------- | ----------- |
| `[agent...]`    | Limit the run to the named agents. |
| `--write`       | Actually edit the agent config files. |

It only removes servers mcphub both proxies **and** previously managed in the
agent (tracked in the intelligence store), so a hand-added entry that happens to
share a name with a proxied server is never clobbered. Anything mcphub does
*not* proxy (disabled or agent-internal servers) is left untouched, and the
`mcphub` gateway itself is never removed. Dry-run by default; `--write` applies
after saving a timestamped `.bak` and updates the managed-entries store.

::: warning Sync first
Run `mcphub sync --write` before offloading so each agent has the mcphub
gateway — `offload` skips any agent that doesn't.
:::

---

## `studio`

Launch the interactive TUI to browse servers, toggle them on and off with
space, and inspect local usage intelligence — then run [`mcphub sync`](#sync)
to push the result to every agent. Alias: `tui`.

```sh
mcphub studio
mcphub tui
```

See [Studio](/guide/studio) for the key bindings and layout.

---

## `stats`

Summarize the tool calls the gateway has recorded: total calls, errors,
estimated token cost, and latency, plus a per-server breakdown by default.

```sh
mcphub stats                    # all-time totals + per-server
mcphub stats --tools            # per-tool breakdown (which exact tools cost the most)
mcphub stats --recent 20        # also list the 20 most recent calls
mcphub stats --since 24h        # limit to a recent window (24h, 90m, 7d, ...)
mcphub stats --server codemap   # drill into one server's tools
mcphub stats --markdown         # a report for notes/issues
mcphub stats --json
```

| Flag | Description |
| --- | --- |
| `--tools` | Break down by individual tool instead of server. |
| `--recent <N>` | Also list the N most recent calls. |
| `--since <window>` | Limit to a recent window, e.g. `24h`, `90m`, `7d` (default: all time). |
| `--server <name>` | Filter to one server's stats and tools. |
| `--markdown` | Render as Markdown (great for notes/issues). |

`--since` accepts any Go duration (`24h`, `90m`) plus a day suffix (`7d`), and
scopes the totals and breakdowns to that lookback window — useful for "which
servers are earning their keep *lately*". With `--json`, the output includes
the totals plus per-server **and** per-tool breakdowns (and recent calls when
`--recent` is set). See [Intelligence](/guide/intelligence) for what the
numbers mean.

---

## `doctor`

Diagnose your setup: that your config parses, every enabled server's command is
on `PATH`, each agent target exists, and the intelligence store opens. mcphub
checks, in order:

- **config** — that `mcphub.yaml` loads and validates (reports its path),
- **server:&lt;name&gt;** — for each enabled server, that its command is on
  `PATH` (remote servers are reported as remote),
- **agent:&lt;name&gt;** — for each agent, that its `type` is supported and its
  config file exists (reports path, type, and resolved mode),
- **available:&lt;type&gt;** — for each supported harness whose config file
  exists on disk but isn't wired in `mcphub.yaml` (run [`mcphub
  agents`](#agents) to list all supported types, or `mcphub init --from-agents`
  to auto-wire them),
- **store** — that the intelligence database opens (reports its path),
- **tvault** — when any server uses a `vault`, that `tvault` is on `PATH`,
- **binary** — the path to the running mcphub executable.

```sh
mcphub doctor
mcphub doctor --probe   # also connect to each server for real
mcphub doctor --json
mcphub doctor --server cortex --probe --json   # one server, real handshake
```

| Flag | Description |
| --- | --- |
| `--probe` | Actually connect to each enabled server and report its tool count. |
| `--server <name>` | Scope to one server: a single-server registration/routing/usage summary. |

Each check prints a `✔`/`✗` mark and a detail line. If any check fails, doctor
exits non-zero. With `--json`, the checks are emitted as structured data.

### `--probe`

`--probe` goes beyond a `PATH` lookup: it actually **spawns each enabled
server, performs the MCP handshake, and lists its tools**, reporting a
`probe:<name>` line with the tool count (or the connection error). It's the
difference between "the binary exists" and "the server actually works":

```
✔ probe:codemap     29 tools
✗ probe:broken      connect: exec: "broken-mcp": executable file not found in $PATH
```

Because it launches every server, `--probe` is slower than a plain `doctor` —
use it when you suspect a server is misconfigured or failing to start.

### `--server`

`--server <name>` scopes `doctor` to a single server — a cheap "am I wired into
the gateway correctly?" answer in one call instead of fetching and joining the
full `list`, `doctor`, and `status` JSON inventories. It reports the server's
registration, enabled state, PATH availability, the agents that route to it,
and how many calls the gateway has proxied to it. With `--probe` it also
performs the real handshake and adds `handshake_ok` + `tool_count`. With
`--json`, the scoped object is:

```json
{"server":"cortex","registered":true,"enabled":true,"on_path":true,
 "handshake_ok":true,"tool_count":8,
 "agents":[{"agent":"claude","mode":"gateway","state":"in sync"}],
 "proxied_calls":0}
```

`handshake_ok` and `tool_count` are only present with `--probe`; an
unregistered server yields `{"server":"<name>","registered":false}`.
[`status --server`](#status) emits the same shape without the probe fields.

---

## `agents`

List every agent harness mcphub can sync to, whether each one's config file
exists on disk, and whether it's already wired into `mcphub.yaml`.

```sh
mcphub agents
mcphub agents --json
```

States:

| State           | Meaning |
| --------------- | ------- |
| `configured`    | Already in `mcphub.yaml` (and the config file exists). |
| `available`     | Not in `mcphub.yaml`, but the config file exists — add it to sync. |
| `not_installed` | Neither in `mcphub.yaml` nor on disk — install the tool first. |

To add an `available` agent, add an entry under `agents:` in `mcphub.yaml`, or
run `mcphub init --from-agents` to auto-discover all installed agents at once.

---

## `mcp serve`

Run mcphub as an MCP server — the gateway. It connects to every enabled
downstream server, aggregates their tools under `server__tool` names, and
records each proxied call to the local intelligence db. This is the server
agents point at in [gateway mode](/guide/concepts#gateway-vs-direct).

```sh
mcphub mcp serve
mcphub mcp serve --agent codex   # scope tools to one agent's servers/tools allowlists
```

| Flag           | Description |
| -------------- | ----------- |
| `--agent <name>` | Scope advertised tools (and the `mcphub_*` meta-tools) to this agent's `servers`/`tools` allowlists from `mcphub.yaml`, instead of advertising everything. |

You normally don't run this by hand — the agent launches it, because that's
what [`mcphub sync`](#sync) writes into the agent's config in gateway mode.
(For an agent with per-agent `servers`/`tools` routing, sync writes
`mcphub mcp serve --agent <name>` so the gateway advertises only that agent's
subset and refuses out-of-scope calls.) Logs go to stderr so they never corrupt
the stdio JSON-RPC stream. It shuts down cleanly on `SIGINT`/`SIGTERM`.

The gateway also exposes seven management meta-tools to connected agents:
`mcphub_list_servers`, `mcphub_search_tools`, `mcphub_describe_tool`,
`mcphub_resolve_tool`, `mcphub_call_tool`, `mcphub_get_result`, and
`mcphub_stats`. With `expose: lazy` in `mcphub.yaml`, those seven plus any
[pinned](#pin) tools are the only tools advertised. `mcphub_get_result` accepts
`{callId, cursor}` and returns a base64 page; continue with `nextCursor` until
`done` is true. See the [meta-tools reference](/reference/meta-tools) for each
tool's inputs and outputs.

---

## See also

- [Configuration reference](/reference/config) — the full `mcphub.yaml` schema.
- [Sync to your agents](/guide/sync) — how the harness adapters merge.
- [Intelligence](/guide/intelligence) — the telemetry and SQLite store.
