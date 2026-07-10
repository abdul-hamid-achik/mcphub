# CLI reference

Every mcphub command, with its flags. Run `mcphub <command> --help` for the
same information at the terminal.

```
mcphub [command] [flags]
```

## Persistent flags

These apply to every command:

| Flag              | Description                                                                 |
| ----------------- | --------------------------------------------------------------------------- |
| `--config <path>` | Path to `mcphub.yaml`. Default: `./mcphub.yaml` or `~/.config/mcphub/mcphub.yaml`. |
| `--db <path>`     | Path to the intelligence SQLite db. Default: `~/.local/share/mcphub/mcphub.db`.    |
| `--json`          | Emit machine-readable JSON where supported.                                 |
| `-v`, `--version` | Print the mcphub version and exit.                                          |
| `-h`, `--help`    | Help for the command.                                                       |

### Environment variables

| Variable        | Overrides           |
| --------------- | ------------------- |
| `MCPHUB_CONFIG` | the config path     |
| `MCPHUB_DB`     | the intelligence db |

The config default resolves in order: `$MCPHUB_CONFIG`, then a `mcphub.yaml` in
the current directory, then `~/.config/mcphub/mcphub.yaml`.

## Commands at a glance

| Command                          | What it does                                            |
| -------------------------------- | ------------------------------------------------------- |
| [`init`](#init)                  | Write a starter `mcphub.yaml` (or `--from-agents` to import existing). |
| [`list`](#list) (`ls`)           | List configured servers.                                |
| [`add`](#add)                    | Register a server (`add <name> [cmd args...]` or `--url`; `--enabled`/`--disabled`). |
| `remove` (`rm`)                  | Offload a server from `mcphub.yaml`.                   |
| [`enable`](#enable-disable)      | Enable a server in `mcphub.yaml`.                       |
| [`disable`](#enable-disable)     | Disable a server in `mcphub.yaml`.                      |
| `groups`                         | List server groups.                                     |
| `use`                            | Enable every server in a group (`--only` to disable the rest). |
| [`pin`](#pin-unpin) / `unpin`    | Keep tools directly callable in lazy mode (`--top N` auto-pins most-used). |
| [`status`](#status)              | Per-agent sync drift + usage intelligence (`--server` scopes to one server). |
| [`sync`](#sync)                  | Write server config into agent harnesses (dry-run by default). |
| [`offload`](#offload)            | Remove gateway-proxied servers from agents, leaving just `mcphub`. |
| [`studio`](#studio) (`tui`)      | Launch the interactive TUI.                            |
| [`stats`](#stats)                | Show local tool-call intelligence (`--tools`, `--recent N`). |
| [`doctor`](#doctor)              | Diagnose config, servers, and agents (`--server` scopes to one, `--probe` connects). |
| [`mcp serve`](#mcp-serve)        | Run the gateway MCP stdio server.                      |
| `completion`                     | Generate a shell autocompletion script.                |
| `help`                           | Help about any command.                                |

---

## `init`

Write a starter `mcphub.yaml` with example servers and six agent harnesses
pre-wired (the starter seeds Claude, opencode, Codex, Crush, Forge, Hermes;
five more — copilot, qwen, gemini, kilo, kimi — are available when you install
them; run `mcphub agents` to see every supported type and its status).

```sh
mcphub init
mcphub init --force
```

| Flag      | Description                                  |
| --------- | -------------------------------------------- |
| `--force` | Overwrite an existing config.                |

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
| `--url <u>` | Register a remote server (instead of a command). |
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

## `enable` / `disable`

Toggle a server's `enabled` flag in `mcphub.yaml`. Each takes exactly one server
name.

```sh
mcphub enable <server>
mcphub disable <server>
```

This edits only the config — it does not touch your agents. Run
[`mcphub sync`](#sync) afterwards to apply the change to your harnesses. An
unknown server name is rejected with a pointer to `mcphub list`.

---

## `sync`

Reconcile every agent harness with `mcphub.yaml`. **Dry run by default** — it
prints the diff it would apply and changes nothing. Pass `--write` to actually
edit the files (a timestamped `.bak` is written first).

```sh
mcphub sync                  # dry run, all agents
mcphub sync --write          # apply
mcphub sync claude codex     # limit scope to named agents
```

| Argument / flag | Description                                                          |
| --------------- | ------------------------------------------------------------------- |
| `[agent...]`    | One or more agent names to sync. With none, all agents are synced.  |
| `--write`       | Actually edit the agent config files (a `.bak` is saved first).     |

In **gateway** mode an agent is given a single `mcphub` server that proxies the
rest. In **direct** mode every enabled server is written into the agent. Agents
marked `disabled: true` are skipped. See [Sync to your agents](/guide/sync) for
how each harness adapter merges.

---

## `pin` / `unpin`

Keep tools mounted directly on the gateway even under `expose: lazy`, so agents
call them automatically instead of going through `mcphub_search_tools` first. A
pin is a whole server, a `server__*` wildcard, or a single `server__tool`.

```sh
mcphub pin codemap vecgrep            # pin two whole servers
mcphub pin codemap__codemap_semantic  # pin one tool
mcphub pin --top 8                    # auto-pin your 8 most-called tools (from stats)
mcphub pin                            # list current pins
mcphub unpin codemap                  # remove a pin
```

In gateway mode no `sync` is needed — the change takes effect the next time the
gateway starts, so restart your agents to pick it up. (In Studio, press `p` on a
server to pin/unpin it.)

---

## `offload`

The second half of "register and offload": remove the direct copies of the
servers mcphub now proxies from each gateway-mode agent, so the agent relies
purely on the single `mcphub` gateway. This is where the token savings land.

```sh
mcphub offload          # dry-run: show what would be removed from each agent
mcphub offload --write  # apply (a .bak is saved per file first)
```

It only removes servers mcphub actually proxies (the enabled ones in your
config); anything it does not proxy (disabled or agent-internal servers) and the
`mcphub` gateway itself are left untouched. Agents in `direct` mode, or that
don't yet have the gateway, are skipped.

---

## `studio`

Launch the interactive TUI to browse servers, toggle them on and off, and
inspect usage. Alias: `tui`.

```sh
mcphub studio
mcphub tui
```

See [Studio](/guide/studio) for the key bindings and layout.

---

## `status`

Answer "is everything consistent?" in one screen. For each agent, `status` does
a read-only dry run and reports whether its on-disk MCP config already matches
`mcphub.yaml` (`in sync`) or has changes pending. It also summarizes recorded
usage and flags **enabled servers that have never been called** — candidates to
disable so your agents carry less context.

```sh
mcphub status
mcphub status --json
mcphub status --server cortex   # scope to one server: routing + proxied calls
```

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
`--json` the [scoped object](#doctor---server) is the same shape `doctor
--server` emits (without the probe fields).

---

## `stats`

Show local tool-call intelligence recorded by the gateway: total calls, errors,
estimated token cost, and total latency, plus a per-server breakdown.

```sh
mcphub stats              # all-time totals + per-server
mcphub stats --tools      # per-tool breakdown (which exact tools cost the most)
mcphub stats --recent 20  # also list the 20 most recent calls
mcphub stats --since 24h  # limit to a recent window (24h, 90m, 7d, ...)
mcphub stats --json
```

`--since` accepts any Go duration (`24h`, `90m`) plus a day suffix (`7d`), and
scopes the totals and breakdowns to that lookback window — useful for "which
servers are earning their keep *lately*". With `--json`, the output includes
the totals plus per-server **and** per-tool breakdowns (and recent calls when
`--recent` is set). See [Intelligence](/guide/intelligence) for what the numbers
mean.

---

## `doctor`

Diagnose your setup. mcphub checks, in order:

- **config** — that `mcphub.yaml` loads and validates (reports its path),
- **server:&lt;name&gt;** — for each enabled server, that its command is on
  `PATH` (remote servers are reported as remote),
- **agent:&lt;name&gt;** — for each agent, that its `type` is supported and its
  config file exists (reports path, type, and resolved mode),
- **available:&lt;type&gt;** — for each supported harness whose config file
  exists on disk but isn't wired in `mcphub.yaml` (run `mcphub agents` to list
  all supported types, or `mcphub init --from-agents` to auto-wire them),
- **store** — that the intelligence database opens (reports its path),
- **tvault** — when any server uses a `vault`, that `tvault` is on `PATH`,
- **binary** — the path to the running mcphub executable.

```sh
mcphub doctor
mcphub doctor --probe   # also connect to each server for real
mcphub doctor --json
mcphub doctor --server cortex --probe --json   # one server, real handshake
```

Each check prints a `✔`/`✗` mark and a detail line. If any check fails, doctor
exits non-zero. With `--json`, the checks are emitted as structured data.

### `--probe`

`--probe` goes beyond a `PATH` lookup: it actually **spawns each enabled server,
performs the MCP handshake, and lists its tools**, reporting a `probe:<name>`
line with the tool count (or the connection error). It's the difference between
"the binary exists" and "the server actually works":

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

`handshake_ok` and `tool_count` are only present with `--probe`; an unregistered
server yields `{"server":"<name>","registered":false}`. `status --server` emits
the same shape without the probe fields.

---

## `mcp serve`

Start the gateway MCP stdio server. It connects to every enabled downstream
server, aggregates their tools under `server__tool` names, and records each
proxied call to the local intelligence db. This is the server agents point at in
[gateway mode](/guide/concepts#gateway-vs-direct).

```sh
mcphub mcp serve
```

You normally don't run this by hand — the agent launches it, because that's what
[`mcphub sync`](#sync) writes into the agent's config in gateway mode. Logs go to
stderr so they never corrupt the stdio JSON-RPC stream. It shuts down cleanly on
`SIGINT`/`SIGTERM`.

The gateway also exposes seven management tools to connected agents:
`mcphub_list_servers`, `mcphub_search_tools`, `mcphub_describe_tool`,
`mcphub_resolve_tool`, `mcphub_call_tool`, `mcphub_get_result`, and `mcphub_stats`.
With `expose: lazy` in `mcphub.yaml`, those seven plus any explicitly pinned tools are the only
tools advertised. `mcphub_get_result` accepts `{callId, cursor}` and returns a base64 page;
continue with `nextCursor` until `done` is true. See
[Concepts](/guide/concepts#management-tools).

---

## See also

- [Configuration reference](/reference/config) — the full `mcphub.yaml` schema.
- [Sync to your agents](/guide/sync) — how the harness adapters merge.
- [Intelligence](/guide/intelligence) — the telemetry and SQLite store.
