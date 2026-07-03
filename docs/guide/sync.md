# Sync to your agents

`mcphub sync` reconciles every agent harness with `mcphub.yaml`. It is how you
stop hand-editing each agent's config file one by one: edit the single
source of truth, then push it everywhere.

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
Supported types: **`claude`**, **`opencode`**, **`codex`**, **`crush`**, **`forge`**, **`hermes`**, **`copilot`**, **`qwen`**, **`gemini`**, **`kilo`**, **`kimi`**.

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

### Crush (`type: crush`)

Target: `~/.config/crush/crush.json`. MCP servers live under the top-level
**`mcp`** object. Each entry carries an explicit `type` (`"stdio"` | `"http"` |
`"sse"`) alongside `command`/`args` or `url`. As with the other JSON adapters,
every other key is preserved byte-for-byte.

### Forge / forgecode (`type: forge`)

Target: `~/forge/.mcp.json` (Forge's own convention is a project-local
`.mcp.json`, but mcphub's default path is the home-relative
`~/forge/.mcp.json` — set the agent's `path` if you keep it elsewhere). MCP
**`mcpServers`** object — the same shape Claude uses — except each entry carries
a `disable` boolean rather than a `type` tag. Other JSON keys are preserved
byte-for-byte.

### Hermes (`type: hermes`)

Target: `~/.hermes/config.yaml`. MCP servers live under the top-level
**`mcp_servers`** map; each entry carries an `enabled` flag. Only the
`mcp_servers` subtree is logically changed and a timestamped `.bak` is always
written first. **Caveat (like Codex):** the YAML is round-tripped through a
generic map, so on a write the whole file is reserialized — every key's *value*
is preserved, but comments and key ordering elsewhere are not.

### GitHub Copilot CLI (`type: copilot`)

Target: `~/.copilot/mcp-config.json`. MCP servers live under the top-level
**`mcpServers`** object — the same shape Claude uses — except every entry
carries an explicit `type` (`"local"`/`"stdio"` | `"http"` | `"sse"`). Entries
may also include `tools`, `headers`, and `timeout` keys; these are left
untouched as unmodeled. Other JSON keys are preserved byte-for-byte.

### Qwen Code (`type: qwen`)

Target: `~/.qwen/settings.json`. MCP servers live under the top-level
**`mcpServers`** object. Qwen distinguishes transport by field name rather than
a type tag: stdio uses `command`+`args`, HTTP uses `httpUrl`, and SSE uses
`url`. Extra keys (`headers`, `timeout`, `trust`, …) are preserved as
unmodeled. Other JSON keys are preserved byte-for-byte.

### Gemini CLI (`type: gemini`)

Target: `~/.gemini/settings.json`. MCP servers live under the top-level
**`mcpServers`** object. Gemini uses the same field-name convention as Qwen:
stdio → `command`+`args`, HTTP → `httpUrl`, SSE → `url`. Extra keys (`headers`,
`timeout`, `trust`, `includeTools`, …) are preserved as unmodeled. Other JSON
keys are preserved byte-for-byte.

### Kilo Code (`type: kilo`)

Target: `~/.config/kilo/kilo.jsonc`. MCP servers live under the top-level
**`mcp`** object. Kilo uses `type: "local"`/`"remote"`, flattens command+args
into a single `command` **array**, and names the env map `environment` — the
same entry shape as opencode. **Caveat:** the file is JSONC (JSON with
comments); mcphub strips comments before parsing so `.jsonc` reads the same as
`.json`, but **comments are not preserved on write** (a `.bak` is taken first,
matching the codex/hermes caveat). Other JSON keys are preserved byte-for-byte.

### Kimi Code CLI (`type: kimi`)

Target: `~/.kimi/config.toml`. MCP servers live under the **`[mcp_servers.*]`**
tables. Kimi uses `type: "local"`/`"remote"`, flattens command+args into a
`command` **array**, and names the env map `environment` — the same entry shape
as opencode/kilo but in TOML. **Caveat (like Codex):** TOML is round-tripped
through a generic map, so comments and key ordering in the file are not
preserved. A timestamped `.bak` is always written before mutating, and `sync`
defaults to dry-run, so this is safe but worth knowing.

### The five newer types

`claude`, `opencode`, `codex`, `crush`, `forge`, and `hermes` are the original
harnesses that `mcphub init` seeds into a starter `mcphub.yaml`. The five
newer types — **`copilot`**, **`qwen`**, **`gemini`**, **`kilo`**, and **`kimi`**
— are registered and fully supported (they sync just like the others) but are
**not seeded by default**. To use one, add an `agents:` entry for it to
`mcphub.yaml` when you install the corresponding tool, or run
`mcphub init --from-agents` to auto-discover any whose config files already
exist on disk. `mcphub agents` lists every type with its status
(configured / available / not_installed), and `mcphub doctor` reports
`available:` entries for types that have a config file but are not yet in
`mcphub.yaml`.

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
