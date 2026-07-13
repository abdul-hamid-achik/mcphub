---
title: Troubleshooting
description: Diagnose mcphub with doctor and --probe, fix common failure modes like PATH issues, missing agent config, and sync drift, and recover from a bad sync.
---

# Troubleshooting

Start every investigation with `mcphub doctor`. Almost everything below is
either something doctor already caught, or something you can confirm by
reaching for `--probe` or `--json`.

## `mcphub doctor`

```sh
mcphub doctor
```

doctor checks, in order: that `mcphub.yaml` parses, that every *enabled*
server's `command` is on `PATH` (remote servers are listed with their URL
instead — there's no binary to find), that each agent's config target exists,
and that the intelligence store opens. If any server injects secrets with
`vault:`, it also verifies `tvault` itself is on `PATH`. Each line is `✔` or
`✗`:

```
✔ config             /Users/you/.config/mcphub/mcphub.yaml
✗ server:ghost        command not on PATH: this-binary-does-not-exist
✗ agent:claude        path not found: ./does-not-exist/claude.json
✔ available:opencode  config file exists but not in mcphub.yaml — add it or run 'mcphub init --from-agents'
✔ store               /Users/you/.local/share/mcphub/mcphub.db
✔ binary              /opt/homebrew/bin/mcphub
```

An `available:<type>` line isn't a failure — it means that harness's config
file exists on disk but isn't wired into `mcphub.yaml` yet. Add an `agents:`
entry for it, or run `mcphub init --from-agents` to pick up every installed
harness at once.

Scope to one server for a focused registration/routing/usage summary:

```sh
mcphub doctor --server codemap
```

### `--probe`: an actual connectivity check

The default checks only prove a server's binary *exists* — they don't prove
it can speak MCP. `--probe` goes further: it spawns
every enabled server, performs the real MCP handshake, and reports how many
tools it exposes, or exactly why the handshake failed:

```sh
mcphub doctor --probe
```

```
✗ probe:vecgrep   connect: calling "initialize": invalid character 'h' looking for beginning of value
```

That particular error means the process started but didn't speak JSON-RPC on
stdout at all — see [stdio hygiene](#stdio-hygiene-logs-must-go-to-stderr)
below, it's almost always the cause.

::: tip Scripting doctor
`--json` works on `doctor` too, and returns the same checks as an array of
`{name, ok, detail}` objects — pipe it into CI or a status page:

```sh
mcphub doctor --json
```

doctor exits non-zero when any check fails, so it works as a CI gate with or
without `--json`.
:::

## Common failure modes

### Server binary not on PATH

`doctor` reports `command not on PATH: <cmd>`. mcphub resolves each stdio
server's `command` the same way your shell would — if it's not an absolute
path, it has to be discoverable in the `PATH` of the process running
`mcphub` (or `mcp serve`, when an agent launches it). Fixes, in order of
preference:

- Put the binary on `PATH` for the shell/session mcphub runs in.
- Use an absolute path in `mcphub.yaml` instead of a bare command name (see
  the `bob` entry in the [configuration reference](/reference/config) for an
  example).
- If the agent itself spawns `mcphub mcp serve` (gateway mode), remember
  *that* process needs the downstream binaries on its `PATH` too — a GUI app
  launched outside your shell's profile often has a much smaller `PATH` than
  your terminal.

### Agent config path missing

`doctor` reports `path not found: <path>`. This means the harness itself
isn't installed yet, or its config lives somewhere other than mcphub's
default guess (`~/.claude.json`, `~/.codex/config.toml`, …). `sync` can't
create a *directory* that doesn't exist yet in every case — check the
[supported harnesses table](/guide/sync#the-harness-adapters) for the
default path per `type`, and override `path:` in that agent's entry in
`mcphub.yaml` if yours lives elsewhere. `mcphub agents` shows this same
signal per harness (`configured` / `available` / `not_installed`).

### Sync drift

"Drift" is when `mcphub.yaml` says one thing and an agent's on-disk config
says another — usually because you edited the YAML (enabled a server,
flipped a mode) but haven't run `sync --write` since. `mcphub status` surfaces
it directly:

```sh
mcphub status
```

```
AGENT   TYPE    MODE     SYNC
claude  claude  gateway  2 pending

Some agents are out of sync. Run `mcphub sync` to preview, `mcphub sync --write` to apply.
```

A `SYNC` column of `in sync` means that agent's file already matches what
`mcphub.yaml` wants; `N pending` is the count of add/update/remove changes
sync would make. Resolve it the same way the message says:

```sh
mcphub sync claude            # preview the diff for just this agent
mcphub sync claude --write    # apply it (a .bak is saved first)
```

### The gateway isn't picked up until you restart the agent

If you just ran `mcphub sync --write`, enabled a server, or changed a
[pin](/guide/concepts#exposure-all-vs-lazy), and your agent doesn't seem to
see the change — restart it. Agents read their MCP config (and launch
`mcphub mcp serve`) once, at startup; they don't hot-reload it. This applies
to:

- A first-time `sync --write` that adds the `mcphub` entry to an agent.
- Enabling/disabling a downstream server in `mcphub.yaml` — the gateway
  connects to enabled downstreams when *it* starts, which happens when the
  agent (re)launches it.
- `mcphub pin` / `mcphub unpin` — no sync is needed for pins, but the running
  gateway process still needs to restart to pick up the new pin set.

If a restart doesn't fix it, confirm the config actually changed
(`mcphub status`) and that the gateway itself starts cleanly
(`mcphub mcp serve` by hand, see below).

### stdio hygiene: logs must go to stderr

`mcphub mcp serve` speaks newline-delimited JSON-RPC on **stdout** — that's
the whole protocol. Its own logs go to **stderr**, on purpose, so nothing
else can interleave with the wire format. If you ever see a downstream
server logging to stdout (a `print()`/`console.log()` left in by mistake,
for instance), the gateway's connection to it breaks with a message like:

```
connect: calling "initialize": invalid character 'h' looking for beginning of value
```

`invalid character '<c>' looking for beginning of value` is the JSON decoder
choking on plain text it expected to be a JSON-RPC frame. Run the offending
server standalone and check what it writes to stdout on startup; a well
behaved MCP stdio server should write nothing there but protocol frames. You
can sanity-check the gateway itself the same way — run it by hand and
confirm stdout stays silent until a client actually calls it:

```sh
mcphub mcp serve
# stdout: nothing yet (it's waiting for a client on stdin)
# stderr: startup logs, e.g. "downstream unavailable server=... err=..."
```

Press `Ctrl-C` once you've confirmed it started without errors — in normal
use your agent launches and owns this process, you don't run it directly.

## Config and DB paths

mcphub reads two paths on every invocation. Both resolve the same way:
explicit flag, then environment variable, then default.

| What            | Flag       | Env             | Default                                           |
| ---------------- | ---------- | --------------- | -------------------------------------------------- |
| Config           | `--config` | `MCPHUB_CONFIG` | `./mcphub.yaml`, else `~/.config/mcphub/mcphub.yaml` |
| Intelligence DB  | `--db`     | `MCPHUB_DB`     | `~/.local/share/mcphub/mcphub.db`                   |

::: warning A silent config mismatch is the most common "it worked yesterday"
If `mcphub sync` / `doctor` / `stats` suddenly look empty or stale, check
you're not accidentally reading a different `mcphub.yaml` than you think —
a `./mcphub.yaml` in your current directory takes priority over
`~/.config/mcphub/mcphub.yaml`, and a leftover `MCPHUB_CONFIG` or
`MCPHUB_DB` exported in a shell profile silently overrides both. Run
`mcphub doctor` — the very first line it prints is the config path it
actually resolved.
:::

Use the flags or env vars to point mcphub at an alternate config/db
explicitly — handy for testing a change before it touches your real setup:

```sh
mcphub --config ./staging.yaml --db ./staging.db doctor
# or
MCPHUB_CONFIG=./staging.yaml MCPHUB_DB=./staging.db mcphub sync
```

## Restoring from a backup

Every `sync --write` saves a timestamped backup of the file it's about to
touch, *before* touching it: `<path>.bak-<YYYYMMDD-HHMMSS>` (for example
`~/.claude.json.bak-20260628-143012`, UTC), sitting right next to the real
config. If nothing needs fixing these just accumulate quietly — ordinary
housekeeping, safe to delete once you're confident in a sync.

**The reliable way to undo a bad write** is to copy the backup back over the
live file yourself:

```sh
ls ~/.claude.json.bak-*                      # find the one you want
cp ~/.claude.json.bak-20260628-143012 ~/.claude.json
```

`sync --rollback <planId>` is meant to do exactly this automatically, keyed
off the plan ID a sync run reports (`plan_<timestamp>_<agent>`):

```sh
mcphub sync --rollback plan_1234567890_claude   # restore that agent's backup
mcphub sync --resume plan_1234567890_claude     # re-sync that agent, with --write
```

::: danger `--rollback` may fail with "no backup found"
In testing, `--rollback` reported `no backup found for <path>` even though
the matching `.bak-<timestamp>` file was sitting right there — see the
[full explanation in Sync](/guide/sync#replaying-or-undoing-a-sync-resume-rollback).
Until it's fixed, use the manual `cp` above; it always works, since it
doesn't depend on the same lookup.
:::

`--resume` isn't affected by that issue — it only needs the agent name
embedded in the plan ID, which it uses to re-run `mcphub sync <agent>
--write`.

## FAQ

**`mcphub sync` says "unchanged" for every agent, but I just edited
`mcphub.yaml` — did my edit not save?** Check `mcphub list` reflects the edit
first. If it does, you may be looking at a different config than you
edited — see [config and DB paths](#config-and-db-paths).

**Do I need to run `sync` after every `enable`/`disable`/`pin`?** For
`enable`/`disable` in **direct** mode, yes — that changes which servers get
written into the agent. In **gateway** mode, `enable`/`disable` and `pin`
changes only affect what the *running gateway* connects to and advertises —
no sync needed, just restart the agent so it relaunches `mcp serve`. `sync`
is only needed in gateway mode the first time (to write the `mcphub` entry
at all), or when you add/remove an *agent* or switch its `mode`.

**`doctor` passes but my agent still can't see any tools.** Confirm the
agent actually launched the gateway (some harnesses cache their tool list
until restart) and that `mcphub mcp serve` run by hand doesn't print an
error to stderr on startup. If it's a [lazy-exposed](/guide/concepts#exposure-all-vs-lazy)
setup, remember the agent only sees the seven `mcphub_*` meta-tools plus any
`pin`s — the rest are reached through `mcphub_search_tools` /
`mcphub_call_tool`, by design, not a bug.

**Can I run `mcphub sync --write` and `mcphub mcp serve` at the same time?**
Yes — `sync` only touches agent config files and the local store; the
gateway doesn't read `mcphub.yaml` again until its next restart, so a sync
while it's running takes effect on the gateway's *next* launch, not
immediately.

**Where do oversized tool results go, and can `doctor` tell me if that store
is unhealthy?** They're stored in the same SQLite database as everything
else (see [Local intelligence](/guide/intelligence) and
[Bounded results](/guide/results)); `doctor`'s `store` check confirms the
database file opens, which is the only failure mode that matters for that
path.

**I hand-added a server to an agent's config outside mcphub — will `sync`
delete it?** No. `sync` only removes entries it previously wrote itself
(tracked in the local store); anything you added by hand is left alone. See
[ownership](/guide/sync#ownership-why-hand-added-servers-survive) in Sync.

## Next

- [Sync to your agents](/guide/sync) — dry-run, backups, and per-harness merge rules.
- [Concepts](/guide/concepts) — gateway vs. direct, namespacing, exposure.
- [CLI reference](/reference/cli) — every flag on `doctor`, `sync`, and `status`.
- [Configuration reference](/reference/config) — `MCPHUB_CONFIG`/`MCPHUB_DB` and every `mcphub.yaml` field.
