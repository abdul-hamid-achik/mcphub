---
title: Fetch HTTP responses with Hitspec
description: Register Hitspec in mcphub, route unpinned HTTP tasks from local-agent, and compose reviewed results with file.cheap explicitly.
---

# Fetch HTTP responses with Hitspec

[Hitspec](https://github.com/abdul-hamid-achik/hitspec) exposes bounded HTTP
fetch, saved-request discovery, and validation over MCP. Its MCP server returns
results inline and **never writes files**. If a response should become a durable
artifact, a trusted host or user must first write the reviewed content to a
workspace file; [file.cheap](https://github.com/abdul-hamid-achik/file.cheap)
can then save that path in a separate operation.

```text
task context → mcphub_resolve_tool → hitspec_fetch → inline result
                                                    ↓ reviewed file handoff
                                              fcheap_save → durable stash
```

This separation matters: routing, HTTP execution, filesystem writes, and
artifact persistence remain distinct policy decisions.

## Prerequisites

Install Hitspec or build it from its repository. If file.cheap persistence is
also needed, install or build `fcheap` separately:

```sh
cd /path/to/hitspec
task build

cd /path/to/file.cheap
task build
```

Choose an absolute API-workspace path for the Hitspec server. The workspace is
the filesystem boundary for saved `.http` / `.hitspec` requests, dotenv files,
config files, discovery, and validation.

## Register Hitspec

Use an absolute binary and workspace path when a GUI or long-running agent may
not inherit your shell's `PATH`:

```sh
mcphub add hitspec \
  --description "Bounded HTTP fetches plus saved Hitspec request discovery and validation" \
  --tag http --tag api --tag markdown --tag testing \
  --use-when "fetch a public HTTP URL as raw, text, Markdown, or JSON" \
  --use-when "list or validate saved .http and .hitspec requests in the API workspace" \
  --use-when "execute one saved Hitspec request without shell hooks or database assertions" \
  -- /absolute/path/to/hitspec mcp serve --workspace /absolute/api-workspace
```

The equivalent registry entry is:

```yaml
servers:
  hitspec:
    command: /absolute/path/to/hitspec
    args: [mcp, serve, --workspace, /absolute/api-workspace]
    enabled: true
    description: Bounded HTTP fetches plus saved Hitspec request discovery and validation
    tags: [http, api, markdown, testing]
    use_when:
      - fetch a public HTTP URL as raw, text, Markdown, or JSON
      - list or validate saved .http and .hitspec requests in the API workspace
      - execute one saved Hitspec request without shell hooks or database assertions
```

If `hitspec` is installed on the gateway process's `PATH`, `command: hitspec`
is sufficient. Keep `--workspace` absolute in either case.

Probe the stdio connection before syncing an agent:

```sh
mcphub doctor --server hitspec --probe
```

The probe verifies the MCP handshake and advertised tools. It does not make an
HTTP request.

## Route local-agent without a pin

Keep the gateway in lazy mode and include Hitspec in local-agent's server
scope, if that agent has an explicit scope:

```yaml
expose: lazy

agents:
  local-agent:
    type: local-agent
    path: ~/.config/local-agent/config.yaml
    mode: gateway
    servers:
      - cortex   # preserve existing routes
      - bob
      - hitspec
```

If `servers` is absent, the agent already inherits every enabled server. Do not
create a `tools` allowlist just for this integration. If one already exists,
append whichever exact tools the agent needs while preserving its other
entries:

```yaml
tools:
  - hitspec__hitspec_fetch
  - hitspec__hitspec_list_requests
  - hitspec__hitspec_validate
```

Preview the generated local-agent config before applying it:

```sh
mcphub sync local-agent
mcphub sync local-agent --write
```

Restart local-agent after the sync. On its next MCP connection, mcphub supplies
bounded initialization instructions and the in-scope `use_when` hints. A task
such as “fetch this public API guide as Markdown” can then resolve to
`hitspec__hitspec_fetch` without pinning the tool or hardcoding Hitspec in the
harness.

See [Contextual routing for harnesses](/guide/contextual-routing) for the task
and phase triggers local-agent should use.

## Tool contract

Hitspec exposes three tools; mcphub prefixes their names with the configured
server name:

| Hitspec tool | Namespaced name | Purpose |
| --- | --- | --- |
| `hitspec_fetch` | `hitspec__hitspec_fetch` | Fetch one direct URL or one saved request as `raw`, `text`, `markdown`, or `json`. |
| `hitspec_list_requests` | `hitspec__hitspec_list_requests` | List request names, methods, source lines, and tags in the fixed workspace. |
| `hitspec_validate` | `hitspec__hitspec_validate` | Parse and structurally validate one workspace file without executing it. |

### Fetch a direct URL

In lazy mode, call the selected downstream tool through `mcphub_call_tool`:

```json
{
  "server": "hitspec",
  "tool": "hitspec_fetch",
  "arguments": {
    "url": "https://example.com/guide?lang=en",
    "format": "markdown"
  }
}
```

For a direct URL, `method`, `headers`, and `body` are optional. The method
defaults to `GET`, or `POST` when `body` is non-empty. `no_follow: true` stops
at the first redirect.

### Fetch a saved request

Use a workspace-relative `.http` or `.hitspec` file instead of `url`:

```json
{
  "server": "hitspec",
  "tool": "hitspec_fetch",
  "arguments": {
    "file": "requests/users.http",
    "name": "get-user",
    "environment": "dev",
    "format": "json"
  }
}
```

Supply exactly one of `url` or `file`. A file containing multiple requests also
requires either a unique `name` or a one-based `index`; those two selectors are
mutually exclusive. `env_file` and `config_file` are workspace-relative.

Saved requests execute only their HTTP request. Hitspec rejects `@depends` and
does not run assertions, captures, conditions, hooks, shell blocks, database
assertions, or `@waitFor`.

### Discover and validate saved requests

Before selecting a request from an unfamiliar workspace, list and validate it:

```json
{
  "server": "hitspec",
  "tool": "hitspec_list_requests",
  "arguments": { "path": "requests" }
}
```

```json
{
  "server": "hitspec",
  "tool": "hitspec_validate",
  "arguments": { "file": "requests/users.http" }
}
```

`hitspec_list_requests` defaults to the workspace root and searches directories
recursively. It returns an error rather than an incomplete list if a discovered
Hitspec file is invalid. All filesystem paths are checked after symlink
resolution.

## Result formats

Every successful Hitspec tool call returns exactly one MCP text-content item;
it does not set structured content.

- `raw` returns a JSON text envelope with sanitized source, status, content
  type, byte size, `encoding: "base64"`, and the response data.
- `text` and `markdown` return the rendered document as text.
- `json` returns Hitspec's machine-safe response envelope serialized as text.
- list and validation results are JSON documents serialized as text.

HTTP `4xx` and `5xx` responses remain inspectable fetch results rather than MCP
transport failures. Machine-safe JSON provenance removes URL credentials,
query strings, fragments, and sensitive authorization or cookie headers.

If the serialized result exceeds mcphub's response budget, mcphub returns a
temporary `callId` receipt. Recover the exact result with
`mcphub_get_result`. That receipt is a transport safeguard with 24-hour
retention, not a file.cheap stash.

## Persist a reviewed response with file.cheap

Register file.cheap only when the agent also needs durable storage or search:

```sh
mcphub add fcheap \
  --description "Local artifact storage, indexing, retrieval, and search" \
  --tag files --tag artifacts --tag search \
  --use-when "save a reviewed workspace file as a durable artifact" \
  --use-when "search or inspect an artifact previously stored in file.cheap" \
  -- /absolute/path/to/fcheap mcp serve
```

Add `fcheap` to local-agent's `servers` scope if that scope is explicit. If it
has an exact `tools` allowlist, add only the required operations, such as
`fcheap__fcheap_save`, `fcheap__fcheap_search`, and `fcheap__fcheap_info`.

The supported handoff is deliberately explicit:

1. Call `hitspec_fetch` and recover any paged result.
2. Review the inline content and provenance.
3. Let a trusted host or user write the accepted content to a known workspace
   path. mcphub does not turn arbitrary MCP output into a file.
4. Describe `fcheap__fcheap_save` if its current schema is not already known.
5. Call `fcheap_save` with the reviewed absolute path.

After the host has created `/absolute/api-workspace/artifacts/example-guide.md`,
the persistence call can be:

```json
{
  "server": "fcheap",
  "tool": "fcheap_save",
  "arguments": {
    "path": "/absolute/api-workspace/artifacts/example-guide.md",
    "name": "Example API guide",
    "tags": ["hitspec-response", "format:markdown"],
    "tool": "hitspec",
    "source": "https://example.com/guide",
    "index": true
  }
}
```

Do not place credentials, cookies, tokens, or URL query parameters in filenames,
tags, or source metadata. Treat the fetch and save as separate effect and
approval boundaries: an HTTP method may have external side effects, while
`fcheap_save` reads a local path and creates durable state.

## Server limits and network policy

Hitspec owns these limits; mcphub preserves the downstream tool contract:

| Server flag | Default | Behavior |
| --- | --- | --- |
| `--workspace` | `.` | Fixed filesystem boundary. Use an absolute path in agent configuration. |
| `--max-body-bytes` | `1048576` | Maximum fetched response body (1 MiB); overflow is an error, not truncation. |
| `--timeout` | `30s` | Maximum duration of one HTTP request. |
| `--allow-private-network` | `false` | Operator grant for private/loopback targets and standard saved-request networking modes. |

By default, direct and saved requests may reach only public HTTP(S) targets.
Hitspec checks the initial target, redirects, DNS answers, and dial target;
rejects URL credentials; limits redirect chains to 10; and removes query
strings from response provenance. Saved requests using configured proxies,
digest, AWS, OAuth2, or multipart modes are rejected under the public-only
policy rather than silently weakened.

To test a trusted local API, the person starting the MCP server must grant that
authority in the registry entry:

```yaml
args:
  - mcp
  - serve
  - --workspace
  - /absolute/api-workspace
  - --allow-private-network
```

This also enables configured proxies and the standard saved-request auth and
multipart modes. Grant it only to agents and workspaces that need it. The
`hitspec_fetch` tool is intentionally not annotated read-only because any HTTP
method can have external effects.

## Troubleshooting

### The old `hitspec_capture_webpage` name does not exist

Current Hitspec advertises `hitspec_fetch`, `hitspec_list_requests`, and
`hitspec_validate`. Rebuild or upgrade the binary, update exact tool
allowlists to the names above, and run `mcphub doctor --server hitspec --probe`.

### `provide exactly one of url or file`

Pass one direct `url` or one workspace-relative saved-request `file`, never
both. Use `name` or `index` only with a saved request.

### A private or localhost target is rejected

Public-only networking is the default SSRF boundary. Add
`--allow-private-network` to the server process only when the operator intends
to grant that authority; a model cannot enable it in tool arguments.

### A saved path is rejected

Use paths relative to the configured Hitspec workspace. Paths that escape the
workspace after symlink resolution are refused.

### The result was not saved in file.cheap

That is expected: Hitspec never writes files and never invokes `fcheap`.
Recover any mcphub-spooled result, review it, create a trusted workspace file,
then call `fcheap_save` explicitly.

### local-agent does not select Hitspec

Confirm the server is connected, the agent scope includes `hitspec`, and an
exact tool scope includes `hitspec__hitspec_fetch` when needed. Keep the tool
unpinned, describe the desired outcome in `use_when`, restart local-agent so it
receives current initialization instructions, and inspect the route with
`mcphub_resolve_tool`.
