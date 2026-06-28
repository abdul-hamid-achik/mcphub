# Sync to your agents

`mcphub sync` reconciles every agent harness with `mcphub.yaml`. It is how you
stop hand-editing `~/.claude.json`, `opencode.json`, and `~/.codex/config.toml`
one by one: edit the single source of truth, then push it everywhere.

```sh
mcphub sync                 # dry run: print the diff, change nothing
mcphub sync --write         # apply it (a .bak is written first)
mcphub sync claude codex    # limit scope to named agents
```

## Dry run by default

`sync` is a **dry run** unless you pass `--write`. A dry run prints the exact
plan it *would* apply — which entries it would add, update, or remove in each
agent — and writes nothing. This is deliberate: you always see the change before
it happens.

```
» claude  (claude, gateway) → /Users/you/.claude.json
    add      mcphub
» opencode  (opencode, direct) → /Users/you/.config/opencode/opencode.json
    unchanged

Dry run. Re-run with --write to apply (a .bak is saved first).
```

Each line shows the agent, its type, its resolved mode, and the target file,
followed by one row per change. `unchanged` rows are listed but count as no-ops.

## Backups

When you run with `--write`, each adapter writes a **timestamped backup** of the
target file *before* mutating it, named:

```
<path>.bak-<YYYYMMDD-HHMMSS>
```

(for example `~/.claude.json.bak-20260628-143012`, in UTC). If the target file
does not exist yet, there is nothing to back up and the adapter simply creates
it. The applied plan reports the backup path it wrote.

## What gets written, per mode

The set of servers mcphub wants present in an agent depends on the agent's
[`mode`](/guide/concepts#gateway-vs-direct):

- **`gateway`** (default) — a single entry named `mcphub` whose command is the
  mcphub binary itself with `mcp serve`. The agent sees one MCP server; mcphub
  proxies the rest.
- **`direct`** — one entry for **every enabled server** in `mcphub.yaml`, copied
  verbatim (command, args, env, url, transport).

## Non-destructive merge

Every adapter does a **safe read-modify-write**. It only ever touches the
MCP-servers section of the file and leaves every other key alone. It also tracks
**which entries mcphub itself wrote** (recorded in the local store), so a later
sync can prune an entry it previously owned — for example a server you disabled,
or all the direct entries after you switch an agent to gateway mode — **without
clobbering servers you added by hand**.

The diff semantics are identical across harnesses (the adapters share one diff
core):

| Action      | Meaning                                                              |
| ----------- | ------------------------------------------------------------------- |
| `add`       | a desired server is not present in the file yet                     |
| `update`    | the server is present but its definition differs                    |
| `remove`    | mcphub previously wrote this server, but no longer wants it          |
| `unchanged` | already matches; nothing to do                                      |

## The harness adapters

Each agent `type` maps to an adapter that knows that harness's on-disk format.
Supported types: **`claude`**, **`opencode`**, **`codex`**.

### Claude Code (`type: claude`)

Target: `~/.claude.json`. MCP servers live under the top-level **`mcpServers`**
object. Only that object is touched; every other key (projects, history, UI
state, …) is preserved verbatim. A stdio server is written as `command` + `args`
+ `env`; a remote server as a `type` (the transport, defaulting to `http`) plus
a `url`.

### opencode (`type: opencode`)

Target: `opencode.json` (commonly `~/.config/opencode/opencode.json`). MCP
servers live under the top-level **`mcp`** object. opencode flattens
command + args into a single `command` **array**, marks each entry with
`type: "local"` or `type: "remote"`, carries an `enabled` flag, and uses
`environment` for env vars.

### Codex (`type: codex`)

Target: `~/.codex/config.toml`. MCP servers live under the
**`[mcp_servers.*]`** tables. mcphub round-trips the TOML through a generic map,
so — heads up — **comments and key ordering in that file are not preserved**.
Only the `mcp_servers` subtree is logically changed; a timestamped `.bak` is
always written first, and sync defaults to dry-run, so this is safe but worth
knowing.

## Scoping and skipping

- With **no agent names**, sync targets all agents in `mcphub.yaml`.
- Pass one or more **agent names** to limit the scope: `mcphub sync claude`.
- An agent marked `disabled: true` is skipped (and reported as skipped) without
  removing its definition from the config.

## Audit trail

Every `--write` sync also appends a row to the local store's audit log (agent,
mode, the server set, and timestamp) and updates mcphub's record of which
entries it manages for that agent. That bookkeeping is what makes the
non-destructive prune above possible. See [Intelligence](/guide/intelligence).

## Next

- [Concepts](/guide/concepts) — gateway vs. direct and token savings.
- [CLI reference](/reference/cli#sync) — the full `sync` surface.
- [Configuration reference](/reference/config#agents) — agent fields.
