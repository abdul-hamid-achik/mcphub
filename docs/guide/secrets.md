---
title: Secrets
description: "Keep API keys and tokens out of mcphub.yaml with TinyVault (tvault) — vault: injects env at spawn for stdio servers, tvault:// resolves remote HTTP headers."
---

# Secrets

`mcphub.yaml` is a file you edit by hand, commit next to dotfiles, and paste
into issues. It should never hold a bearer token. mcphub has two independent
ways to keep secrets out of it, both backed by
[TinyVault](https://github.com/abdul-hamid-achik/tinyvault) (`tvault`):

- **`vault:`** — for **stdio** servers. mcphub spawns the server through
  `tvault run`, which injects the vault project's secrets as environment
  variables. The config only ever names the project.
- **`tvault://` header refs** — for **remote** (`http`/`sse`) servers. A
  `headers` value that starts with `tvault://` is resolved to the real secret
  when the gateway connects. The config only ever holds the reference.

Both beat the alternative — an `env: { API_KEY: sk-... }` sitting in plain
text in `mcphub.yaml` — without changing how you write a server entry.

## `vault:` — spawn-time secrets for stdio servers

Point a server at a tvault project instead of listing its secrets in `env`:

```yaml
servers:
  github:
    command: gh-mcp
    args: [--stdio]
    vault: github            # spawn via `tvault run --project github`
    vault_only: [GH_TOKEN]   # least-privilege allowlist (optional)
    enabled: true
    description: GitHub MCP
```

When mcphub launches this server — from the gateway (`mcphub mcp serve`) or
written verbatim into a `direct`-mode agent — it doesn't run `gh-mcp` directly.
It wraps the command:

```
tvault run --project github --only GH_TOKEN -- gh-mcp --stdio
```

`tvault run` unlocks the vault, injects the project's secrets as environment
variables into the child process, and execs `gh-mcp`. `gh-mcp` sees
`GH_TOKEN` in its environment; `mcphub.yaml` never does.

### Narrowing what gets injected

- **`vault_only`** — a list of secret keys; only these are injected. Least
  privilege: a server that only needs `GH_TOKEN` shouldn't also get every
  other secret in the `github` project. The keys become a single
  comma-joined `--only` flag on the wrapped command.
- **`vault_prefix`** — inject only keys with this prefix instead of an
  explicit list; becomes `--prefix <value>` on the wrapped command.

### Gateway vs. direct mode

- **`gateway`** — the hub spawns the server itself via the wrapped command, so
  the secrets reach the downstream process and are never exposed to the agent
  at all.
- **`direct`** — `sync` writes the *wrapped* command (`tvault run --project
  github --only GH_TOKEN -- gh-mcp --stdio`) straight into the agent's config, so the agent
  launches it the same way. That means `tvault` must be on **the agent's**
  `PATH` too, and the vault must be unlockable in the environment the agent
  runs in — not just yours.

### From the CLI

```sh
mcphub add github gh-mcp --vault github --vault-only GH_TOKEN
```

`--vault` takes the project name; `--vault-only` is repeatable for multiple
keys. There is no `--vault-prefix` flag yet, so set `vault_prefix` by editing
`mcphub.yaml` directly if you need it.

::: warning vault requires a command
`vault` wraps a spawned process, so it only applies to stdio servers
(`command` set). A server with both `vault` and `url` fails `Validate()` —
mcphub rejects the config with `vault injects env into a spawned command and
can't be used with a remote url`.
:::

### Checking it's wired up

`mcphub doctor` looks for `tvault` on `PATH` whenever any enabled server uses
`vault`, and fails the check with `a server uses vault but tvault is not on
PATH` if it isn't (and, when it is, annotates each vaulted server's check with
`(secrets via tvault:<project>)`). `mcphub list` marks a vaulted server with a
`[vault:<project>]` tag next to its command so you can see at a glance which
servers keep their secrets out of the file.

::: tip Unlocking the vault
`tvault run`/`tvault get` need the vault unlocked in whatever process spawns
them — via `TVAULT_PASSPHRASE`, `TVAULT_IDENTITY_KEY`, or a running `tvault
agent`. That's a tvault concern, not mcphub's; mcphub just shells out.
:::

## `tvault://` refs — secrets in remote headers

Remote (`http`/`sse`) servers often need an `Authorization` header rather than
a spawned-process env var. `headers` on a server carries custom HTTP headers
sent with every request; any value that starts with `tvault://` is resolved
by the gateway at connect time instead of being read literally:

```yaml
servers:
  obsidian:
    url: "https://127.0.0.1:27124/mcp"
    transport: http
    headers:
      Authorization: "tvault://obsidian/authorization"
    enabled: true
    description: Obsidian Local REST API
```

Reference syntax:

- **`tvault://<project>/<key>`** — a specific project and key, e.g.
  `tvault://obsidian/authorization`.
- **`tvault://<key>`** — just a key, resolved against tvault's currently
  active project.
- **`tvault://current/<key>`** — `current` is an explicit alias for the active
  project (equivalent to omitting the project).

A header value with no `tvault://` prefix is passed through unchanged, so
plain literal headers still work if you genuinely want one inline.

### How resolution works

When the gateway is about to dial a remote server that has `headers` set, it
resolves every `tvault://` value by shelling out to `tvault get <key> -p <project>` and substitutes the returned secret before opening the
connection. A malformed reference (`tvault://` with nothing after it, or an
empty key after the `/`) or a failed `tvault get` fails that server's
connection with a clear error instead of sending a broken or literal header.

::: warning Gateway-only — direct-mode agents don't get resolved headers
`headers` is resolved by whichever process actually dials the URL. In
**gateway** mode that's mcphub's hub, so resolution always happens. In
**direct** mode the agent connects to the remote server itself, and `sync`
does not carry `headers` into any agent's config at all (it isn't part of the
portable server shape `sync` writes) — a direct-mode agent never sees the
header, resolved or not. If an agent must run in direct mode against a
header-authenticated remote server, configure that header in the agent's own
config instead of relying on mcphub's `headers`/`tvault://` mechanism.
:::

### Validation

`headers` only makes sense on a remote server. A server with `headers` set but
no `url` fails validation with `headers only apply to remote (url) servers`.

## Two mechanisms, one vault, different shapes

| | `vault:` | `headers: tvault://...` |
| --- | --- | --- |
| Applies to | stdio servers (`command`) | remote servers (`url`) |
| Delivers secrets as | environment variables | HTTP header values |
| Resolved by | `tvault run` wrapping the spawn command | the gateway, at connect time |
| Works in `direct` mode | yes — the wrapped command is written verbatim (needs `tvault` on the agent's PATH) | no — headers aren't synced to agent configs |
| Narrowing | `vault_only`, `vault_prefix` | per-header, via the reference itself |

They compose fine on the same `mcphub.yaml` — a stdio server can use `vault`
while a separate remote server uses `tvault://` headers.

## Next

- [Configuration reference](/reference/config#secrets-vault-and-tvault-headers) — the full
  `vault`/`vault_only`/`vault_prefix` field table.
- [Concepts](/guide/concepts#gateway-vs-direct) — what gateway vs. direct mode
  changes about who spawns or dials a server.
- [Sync to your agents](/guide/sync) — why `headers` isn't part of what sync
  writes into an agent's config.
