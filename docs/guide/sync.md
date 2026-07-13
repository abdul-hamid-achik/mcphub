---
title: Sync to your agents
description: How mcphub sync reconciles mcphub.yaml into every agent config — dry-run by default, timestamped backups, surgical merges, and safe ownership tracking.
---

# Sync to your agents

`mcphub sync` reconciles every agent harness with `mcphub.yaml`. It is how you
stop hand-editing `~/.claude.json`, `~/.codex/config.toml`, and the rest one by
one: edit the single source of truth, preview the diff, then push it
everywhere.

```sh
mcphub sync                 # dry run: print the diff, change nothing
mcphub sync --write         # apply it (a .bak is written first)
mcphub sync claude codex    # limit scope to named agents
```

## Dry run by default

`sync` is a **dry run** unless you pass `--write`. It computes the plan — which
entries it would add, update, or remove in each agent — prints it, and writes
nothing:

```
» claude  (claude, gateway) → /Users/you/.claude.json
    add      mcphub
» opencode  (opencode, direct) → /Users/you/.config/opencode/opencode.json
    up to date

Dry run. Re-run with --write to apply (a .bak is saved first).
```

Each line shows the agent, its type, its resolved
[mode](/guide/concepts#gateway-vs-direct), and the target file, followed by one
row per change:

| Action       | Meaning                                                      |
| ------------ | ------------------------------------------------------------- |
| `add`        | a desired server is not present in the file yet               |
| `update`     | the server is present but its definition differs              |
| `remove`     | mcphub previously wrote this server, but no longer wants it   |
| `up to date` | nothing differs for this agent; no per-server rows are listed |

This diff logic is shared by every adapter — the semantics are identical
across harnesses — and it's the same engine the Studio TUI uses for its
preview-and-apply sync panel.

## Applying: `--write` and the backup

Pass `--write` to actually edit the files. Before mutating anything, each
adapter copies the current file to a timestamped backup next to it:

```
<path>.bak-<YYYYMMDD-HHMMSS>
```

for example `~/.claude.json.bak-20260628-143012` (UTC). The timestamp has
one-second resolution, so two syncs in the same second don't clobber each
other's backup — a `-1`, `-2`, … suffix is appended instead. If the target
file doesn't exist yet, there's nothing to back up and the adapter just
creates it. A successful apply reports the backup path it wrote.

## What gets written, per mode

The set of servers mcphub wants present in an agent depends on its
[`mode`](/guide/concepts#gateway-vs-direct):

- **`gateway`** (default) — a single entry named `mcphub` whose command is the
  mcphub binary itself with `mcp serve`. The agent sees one MCP server;
  mcphub proxies the rest.
- **`direct`** — one entry for **every enabled server** in `mcphub.yaml`,
  copied verbatim (command, args, env, url, transport).

## Surgical merge, not a rewrite

Every adapter does a **read-modify-write**, not a wholesale rewrite. It parses
the file, touches only its MCP-servers section — `mcpServers`, `mcp`,
`mcp_servers`, or `[mcp_servers.*]` depending on the harness — and leaves
every other key in the file exactly as it was: your settings, UI state, other
top-level config, all preserved verbatim.

## Ownership: why hand-added servers survive

A `remove` only ever targets servers mcphub itself previously wrote. Every
`--write` sync records **which entries it owns per agent** in the local
store's `managed_entries` table. The next sync prunes only entries in that
set — for example a server you disabled, or the old direct entries after you
flip an agent to `gateway` mode — and never touches a server you added to
that file by hand, even one whose name happens to collide. See
[Intelligence](/guide/intelligence#the-store) for the store's schema.

::: tip
This is what makes `sync` safe to run repeatedly and unattended: it can only
ever converge the file toward what `mcphub.yaml` wants among servers it
manages. Everything else in the file is invisible to it.
:::

## Scoping and skipping

- With **no agent names**, sync targets every agent in `mcphub.yaml`.
- Pass one or more names to limit the run: `mcphub sync claude codex`.
- An agent marked `disabled: true` in `mcphub.yaml` is **skipped** — reported
  as disabled, not touched — without removing its definition from the config.

## The harness adapters

Each agent `type` maps to an adapter that knows that harness's on-disk
format and section key:

| Type      | Target                                | MCP-servers key    |
| --------- | -------------------------------------- | ------------------ |
| `claude`  | `~/.claude.json`                       | `mcpServers`        |
| `opencode`| `~/.config/opencode/opencode.json`     | `mcp`               |
| `codex`   | `~/.codex/config.toml`                 | `[mcp_servers.*]`   |
| `crush`   | `~/.config/crush/crush.json`           | `mcp`               |
| `forge`   | `~/forge/.mcp.json`                    | `mcpServers`        |
| `hermes`  | `~/.hermes/config.yaml`                | `mcp_servers`       |
| `copilot` | `~/.copilot/mcp-config.json`           | `mcpServers`        |
| `qwen`    | `~/.qwen/settings.json`                | `mcpServers`        |
| `gemini`  | `~/.gemini/settings.json`              | `mcpServers`        |
| `kilo`    | `~/.config/kilo/kilo.jsonc`            | `mcp`               |
| `kimi`    | `~/.kimi/config.toml`                  | `[mcp_servers.*]`   |

`claude`, `opencode`, `codex`, `crush`, `forge`, and `hermes` are the harnesses
`mcphub init` seeds by default; `copilot`, `qwen`, `gemini`, `kilo`, and `kimi`
are fully supported but not seeded — add an `agents:` entry for one yourself,
or run `mcphub init --from-agents` to auto-discover any whose config already
exists on disk. See [Configuration reference](/reference/config#agents) for
each adapter's exact entry shape (which fields it reads, `command`+`args` vs.
a flattened `command` array, `httpUrl` vs. `url`, and so on).

### The Codex (and TOML/YAML/JSONC) round-trip caveat

Codex and Kimi Code CLI store MCP servers under TOML `[mcp_servers.*]` tables;
Hermes uses YAML; Kilo Code uses JSONC. mcphub merges all four by
round-tripping the file through a generic map, so **on a write, comments and
key ordering are not preserved** — every key's *value* survives, but the
file's formatting doesn't. The JSON-native harnesses (Claude, opencode,
Copilot CLI, Qwen Code, Gemini CLI, Crush, Forge) are gentler: every unknown
key's *value* is carried through verbatim, though the file is re-emitted as
two-space-indented JSON, so whitespace and top-level key order can still
shift on the first write.

::: warning
None of this is destructive — the `.bak` written before every apply is a
byte-for-byte copy of the original file, comments and all, so nothing is
actually lost. It's just worth knowing before you go looking for a comment
you swear you wrote in `config.toml`.
:::

## Replaying or undoing a sync: `--resume` / `--rollback`

Every plan `sync` computes carries a **plan ID** shaped like
`plan_<timestamp>_<agent>`, printed on the last line of each agent's result
(`plan: plan_1783926023922898000_claude`) in both dry runs and writes. When a
write actually changes a file, the plan ID is recorded in the local store
together with the exact `.bak-<timestamp>` backup that captured the pre-apply
state.

```sh
mcphub sync --resume plan_1783926023922898000_claude     # re-sync just that agent, with --write
mcphub sync --rollback plan_1783926023922898000_claude   # restore that plan's own backup
```

- **`--resume <planId>`** extracts the agent name (everything after the
  second underscore, so agent names containing underscores work) and re-runs
  sync for just that agent with `--write` forced on — equivalent to
  `mcphub sync <agent> --write`, re-applying the current desired config.
- **`--rollback <planId>`** looks the plan up in the store and restores the
  **exact backup recorded for that plan** — not just whatever backup happens
  to be newest — so rolling back an older plan undoes to that plan's
  pre-apply state.

::: warning Unrecorded plans fall back to the newest backup
A plan is only recorded when a write actually changed the file. If the ID
names a dry run, a no-op apply, or a plan from before backup tracking,
`--rollback` says so and restores the agent's most recent
`<path>.bak-<timestamp>` instead. The backups are plain copies sitting next
to the config, so a manual `cp` back over the original always works too.
:::

## Removing the redundant direct copies: `offload`

`sync --write` gives a gateway-mode agent the `mcphub` entry, but it doesn't
by itself remove any servers you'd previously written into that agent
directly. `mcphub offload` is the second half of "register and offload": it
strips out the direct copies of servers mcphub now proxies, so the agent
relies purely on the single `mcphub` gateway — this is where the token
savings actually land, since the agent stops carrying every proxied server's
full tool list.

```sh
mcphub offload            # dry-run: show what would be removed from each agent
mcphub offload claude     # scope to named agents, same as sync
mcphub offload --write    # apply (a .bak is saved per file first)
```

It only removes a server if mcphub both **proxies** it (enabled in
`mcphub.yaml`) and **previously managed** it in that agent (tracked in the
intelligence store) — so a hand-added entry that happens to share a name with
a proxied server is never touched. Anything mcphub doesn't proxy (disabled
servers, or agent-internal ones it never wrote) is left alone, and the
`mcphub` gateway entry itself is never removed. Like `sync`, it is dry-run by
default; `--write` applies after saving a timestamped `.bak` and updates the
`managed_entries` bookkeeping to match.

::: tip
Run `mcphub sync --write` first so each agent actually has the `mcphub`
gateway entry — `offload` skips any agent that doesn't.
:::

## Audit trail

Every `--write` sync appends a row to the local store's audit log (agent,
mode, the server set, and timestamp) and updates the `managed_entries` record
for that agent. That bookkeeping is what makes the non-destructive prune above
possible — so it must never drift from what's on disk. If updating the
ownership store fails *after* a file was written, sync automatically restores
the `.bak` it just took, keeping the config and the bookkeeping consistent.
See [Intelligence](/guide/intelligence).

## Next

- [Concepts](/guide/concepts) — gateway vs. direct and where the token savings come from.
- [Intelligence](/guide/intelligence) — the `managed_entries` and `tool_calls` tables.
- [CLI reference](/reference/cli#sync) — the full `sync` and `offload` flag surface.
- [Configuration reference](/reference/config#agents) — per-harness `type`, `path`, and format table.
