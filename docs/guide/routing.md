---
title: Per-agent routing
description: Route different MCP servers and tools to different agents from one mcphub.yaml — scoped gateways, servers and tools allowlists, and direct-mode filtering.
---

# Per-agent routing

By default, every enabled server in `mcphub.yaml` reaches every non-disabled
agent. That is the right default for one person with one toolbox — but in a
multi-agent setup you often want different agents to see different subsets: a
review agent that only needs code search, a deploy agent that should never see
the database server, a minimal agent that gets nothing but the gateway's
meta-tools.

Per-agent routing does exactly that with two optional lists on an agent entry:

```yaml
agents:
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
    servers: [codemap, vecgrep]                            # which servers it may reach
    tools: [codemap__codemap_find, vecgrep__vecgrep_search] # which exact tools it may call
```

- **`servers`** — which downstream servers this agent may reach.
- **`tools`** — which exact `server__tool` names it may call. **Gateway mode
  only**: a direct agent talks to each server itself, so mcphub has no proxy in
  the path to filter individual tools. `mcphub doctor` rejects `tools` on a
  direct-mode agent.

::: warning Curation, not a security boundary
Routing controls what the gateway *advertises and honors* for a well-behaved
agent — it keeps context lean and agents on-task. It is not a hard isolation
layer: anything that can run `mcphub mcp serve` without `--agent` (or talk to
the downstream servers directly) sees everything. Don't use routing to protect
secrets from a hostile process; use it to stop your review agent burning tokens
on deployment tools.
:::

## Omitted vs empty: the three states

Each list is a *pointer* in the config — absent and empty mean different things:

| In `mcphub.yaml` | Meaning |
| --- | --- |
| key omitted entirely | **All.** No restriction — the pre-routing behavior. |
| `servers: []` / `tools: []` | **None.** A deliberately minimal agent. |
| `servers: [a, b]` / `tools: [a__x]` | **Only these.** |

So an agent with no `servers` and no `tools` keys behaves exactly as before,
and adding routing to one agent never changes what the others see.

```yaml
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
    # no servers/tools keys -> sees every enabled server

  minimal:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: gateway
    servers: []          # sees NO downstream servers, only the mcphub meta-tools
```

A listed server that is disabled (or missing from the enabled set) is dropped
silently — routing selects *within* the enabled servers, it does not enable
anything. A `servers` entry naming a server that doesn't exist in the config at
all is a validation error.

## How it works in gateway mode

When a gateway-mode agent has any routing config (either list present, even
empty), `mcphub sync` writes its harness entry with an extra flag:

```
mcphub mcp serve --agent codex
```

instead of the plain `mcphub mcp serve` that unscoped agents get. When that
gateway starts, it looks up the named agent's `servers`/`tools` allowlists and:

- **Advertises only the subset.** In `expose: all`, only in-scope
  `server__tool` names are mounted. The agent never sees out-of-scope tools, so
  they cost zero context.
- **Scopes the meta-tools too.** In `expose: lazy`, `mcphub_list_servers`,
  `mcphub_search_tools`, `mcphub_describe_tool`, and `mcphub_resolve_tool` only
  surface in-scope servers and tools.
- **Refuses out-of-scope calls.** `mcphub_call_tool` on a tool outside the
  allowlists fails with `tool server__x is out of scope for this agent`, and
  `mcphub_get_result` scope-checks the stored server/tool before paging a
  spooled result.

A bare `mcphub mcp serve` (no `--agent`) is unscoped and serves everything, as
is `--agent <name>` for an agent with no routing keys. An `--agent` naming an
agent that doesn't exist in the config is an error — a stale flag in a harness
file fails fast instead of silently serving everything or nothing.

::: tip Restart to apply
Routing is enforced at gateway startup. After editing `servers`/`tools`, run
`mcphub sync` (the `--agent` flag in the harness entry may need to appear or
disappear) and restart the agent so it relaunches its gateway.
:::

## How it works in direct mode

A direct-mode agent gets no proxy, so routing happens at sync time:
`mcphub sync` writes **only the listed enabled servers** into the agent's
config, verbatim. `servers: []` writes none. `tools` is not allowed — with no
gateway in the path there is nothing to enforce a per-tool list.

```yaml
agents:
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: direct
    servers: [vecgrep]    # only vecgrep is written into opencode.json
```

All the usual [sync guarantees](/guide/sync) still apply: dry-run by default, a
timestamped `.bak` before any write, and pruning scoped to entries mcphub owns.

## `tools` rules

The `tools` list is validated by `mcphub doctor` (and on every config load):

- Entries are exact namespaced names: `server__tool`. **No wildcards** — to
  allow a whole server, put it in `servers` and omit `tools` restrictions for
  it.
- Each entry's server prefix must name a server that exists in the config.
- If the agent also has a `servers` list, every `tools` entry must reference a
  server on that list — a tool is only callable when *both* lists allow it.
- `tools` on a `mode: direct` agent is rejected.

```yaml
agents:
  reviewer:
    type: claude
    path: ~/.claude.json
    mode: gateway
    servers: [codemap]
    tools: [codemap__codemap_find]   # OK: codemap is in servers
    # tools: [vecgrep__search]       # error: vecgrep not in this agent's servers list
    # tools: [codemap__*]            # error: wildcards are not supported
```

## Inspecting routing

```sh
mcphub status --server codemap    # which agents route to codemap + proxied-call count
mcphub doctor --server codemap    # single-server registration/routing/usage summary
mcphub sync                       # dry run: see the exact --agent args and server sets
```

`mcphub sync` (dry run, the default) is the fastest way to confirm what each
agent will actually receive before you `--write` anything. Once calls flow,
[`mcphub stats`](/guide/intelligence) tells you whether a scoped-down agent is
actually using what you left in scope.

## Routing vs the other knobs

Routing composes with mcphub's other context-budget features rather than
replacing them:

- **[`expose: lazy`](/guide/concepts)** shrinks *how* tools are advertised
  (meta-tools + on-demand discovery); routing shrinks *which* tools exist for
  an agent at all. A scoped lazy gateway discovers only in-scope tools.
- **`enabled: false` on a server** removes it for *everyone*; a routing list
  removes it for *one agent*.
- **`disabled: true` on an agent** skips the agent during sync entirely;
  `servers: []` still syncs it, just with an empty (gateway) or absent (direct)
  server set.
