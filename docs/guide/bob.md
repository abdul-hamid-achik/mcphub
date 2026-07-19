# Connect Bob

[Bob](https://github.com/abdul-hamid-achik/bob) is a deterministic repository
factory and lifecycle reconciler. Its MCP server gives agents bounded views of
repository state, plans, manifest validity, the embedded recipe, and local
usage. Bob's MCP tools do not apply repository changes.

## Register the server

Make sure the Bob binary is on `PATH`, then register it once in MCPHub:

```sh
mcphub add bob \
  --description "Deterministic repository factory and lifecycle reconciler" \
  --tag builder \
  --tag code \
  -- bob mcp serve --allow-any-workspace
```

The `--` ends MCPHub's flags. Everything after it becomes Bob's command and
arguments. The resulting server entry is:

```yaml
servers:
  bob:
    command: bob
    args: [mcp, serve, --allow-any-workspace]
    enabled: true
    description: Deterministic repository factory and lifecycle reconciler
    tags: [builder, code]
    use_when:
      - inspect or plan a repository feature before implementation
      - validate repository state against a Bob manifest
```

::: warning Workspace authority
`--allow-any-workspace` lets every agent connected to this gateway ask Bob to
read any existing workspace that the Bob process can read. Use it only for a
trusted, local, single-user gateway.

For least privilege, omit `--allow-any-workspace` and add one repeatable
`--allow-workspace /absolute/path` argument for each repository the gateway may
inspect. Bob also accepts its startup workspace.
:::

## Choose which tools stay visible

Bob exposes six tools. MCPHub prefixes each one with `bob__`:

| MCPHub tool | Purpose |
| --- | --- |
| `bob__bob_inspect` | Inspect Bob-managed state and offline specialist availability. |
| `bob__bob_plan` | Return a bounded repository plan and deterministic digest. |
| `bob__bob_check` | Check convergence, conflicts, and lock drift. |
| `bob__bob_validate_manifest` | Strictly validate a workspace manifest or bounded inline YAML. |
| `bob__bob_recipe_describe` | Describe the embedded recipe contract. |
| `bob__bob_stats` | Summarize Bob's opt-in, local-only usage aggregates. |

With `expose: all`, MCPHub advertises all six automatically. With
`expose: lazy`, Bob remains discoverable through contextual resolution; pin
the tools you also want advertised directly without a catalog lookup:

```sh
mcphub pin bob__bob_inspect bob__bob_plan bob__bob_check \
  bob__bob_validate_manifest bob__bob_recipe_describe bob__bob_stats
```

MCPHub changes only the protocol name and prefixes the description when it
mounts a tool. Bob's title, input and output schemas, annotations, icons, and
`_meta` remain available to the agent.

## Verify the connection

Probe only Bob, then inspect the registration summary:

```sh
mcphub doctor --server bob --probe
mcphub list
```

In gateway mode, restart the agent after changing the registration or pins.
You do not need to sync again for a server-only change because the agent still
launches the same `mcphub mcp serve` gateway. In direct mode, preview and apply
the updated server definition:

```sh
mcphub sync
mcphub sync --write
```

## Usage data

MCPHub records aggregate gateway call intelligence in its local SQLite store.
Bob can separately record opt-in local usage under its XDG state directory and
expose only aggregates through `bob__bob_stats`. Neither feature sends usage
data over the network.

## See also

- [Configuration](/reference/config) — server and routing fields.
- [Concepts](/guide/concepts) — namespacing, lazy exposure, and pins.
- [Sync to your agents](/guide/sync) — gateway and direct mode behavior.
