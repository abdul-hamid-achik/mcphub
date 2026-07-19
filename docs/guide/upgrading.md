---
title: Upgrading
description: What changes when you move from mcphub 0.16.x to 0.18.x — gateway command path, schema budgets, doctor exits, detached calls on SIGTERM, and secret-safe sync details.
---

# Upgrading (0.16 → 0.18)

Most upgrades are additive: default gateway agents without per-agent `pin` or
`tool_schema_budget` keep the same advertised surface. Dry-run sync once after
installing a new version before you apply.

```sh
brew upgrade mcphub   # or reinstall from source
mcphub sync           # dry-run first — never --write until you like the plan
mcphub doctor
```

## One-time gateway `command` path

Since **v0.16.2**, `mcphub sync` records a **stable** gateway binary when the
basename on `PATH` resolves to the same file as the process that ran sync
(`/opt/homebrew/bin/mcphub` instead of a Caskroom versioned path). The Studio
TUI uses the same resolution.

After upgrade you may see a single dry-run `update` on the `mcphub` entry:

```
command "…/Caskroom/…/mcphub" → "/opt/homebrew/bin/mcphub"
```

That is expected. Review with `mcphub sync`, then `mcphub sync --write` once.

## Secret-safe sync details (stay ≥ 0.16.3)

Field-level update detail landed in **v0.16.2**; **v0.16.3** redacts
secret-named flags and URL credentials in those lines. Later builds also mask
docker-style `-e` / `--env` values and URL-shaped args. If any dry-run output
from an early 0.16.2 build was pasted into CI logs, scrub those logs.

## Schema budgets and pin (0.18)

Per-agent `tool_schema_budget` and gateway `pin` shape what is **advertised**
on `tools/list`. They do **not** remove call authority: tools still in
`servers` / `tools` scope remain callable via meta-tools
(`mcphub_search_tools`, `mcphub_call_tool`, …).

- Agents that only invoke *mounted* names may look “broken” under a tight
  budget until they use meta-tools or you raise the budget.
- After changing agent policy, re-run `mcphub sync --write` so gateway entries
  get `--agent <name>`, then restart the harness.
- `mcphub_list_servers` exposes a `tool_schema_budget` diagnostic including
  advertised and omitted tool names when a budget is in effect.

See [Lazy mode & pinning](/guide/lazy-mode) and [Per-agent routing](/guide/routing).

## Live catalog refresh (0.18)

The gateway listens for downstream `tools/list_changed` and remounts under the
same scope and budget. Harnesses that cache tool lists should honor list-changed
notifications (or re-list after a revision drift). A gateway restart still
forgets in-memory detached call IDs (unchanged since 0.15+).

## Detached calls and SIGTERM (0.17)

Stopping the gateway (`SIGTERM` / process exit) **cancels** in-flight
`detach: true` work instead of waiting out the full detached timeout (up to
30 minutes). Poll receipts report failure with an outcome-unknown / no-reconnect
wording. Prefer letting long jobs finish before restarting the gateway.

## Doctor exit codes

Scoped / probe failures can leave doctor with a **non-zero** exit after JSON is
printed. CI that only inspected stdout and ignored exit status may start failing
— treat non-zero as real.

## Recommended checklist

1. `mcphub sync` (dry-run) — expect possible one-time gateway path update  
2. `mcphub sync --write` when the plan looks right  
3. Restart agents that already had an MCP session open  
4. `mcphub doctor` (and `mcphub doctor --probe` if you use scoped agents)  
5. If you use `tool_schema_budget`, verify the advertised set with
   `mcphub_list_servers` / Studio Stats after the gateway is up  

For the full history of each release, see the repository
[CHANGELOG](https://github.com/abdul-hamid-achik/mcphub/blob/main/CHANGELOG.md).
