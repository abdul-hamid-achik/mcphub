---
title: Search, fetch, and capture with Hitspec
description: Register Hitspec in mcphub, route bounded web tasks from local-agent, and persist webpages explicitly through file.cheap.
---

# Search, fetch, and capture with Hitspec

[Hitspec](https://github.com/abdul-hamid-achik/hitspec) exposes bounded HTTP
fetch, saved-request discovery, and validation over MCP. Two server-owned
integrations can extend that core surface: Tavily adds normalized public-web
discovery, and a fixed [file.cheap](https://github.com/abdul-hamid-achik/file.cheap)
executable adds explicit durable webpage capture. Fetch remains inline and
non-persistent; capture is the named operation that writes an explicit durable
Markdown artifact.

```text
task context → mcphub_resolve_tool
                    ├─ hitspec_search_web      → discovery candidates
                    ├─ hitspec_fetch           → bounded inline result
                    └─ hitspec_capture_webpage → durable Markdown stash
```

This separation matters: routing, discovery, retrieval, and durable persistence
remain distinct policy decisions. Search snippets are candidates, not verified
evidence; use fetch for bounded inspection and capture only when persistence is
intended.

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

### Split core capabilities from protected web discovery

Hitspec can run as two independently registered server processes. This is a
useful degradation pattern when only an optional extension needs a secret: keep
the non-secret core (and optional durable capture) available, and put only
Tavily-backed discovery behind TinyVault.

```yaml
servers:
  # No secret dependency. With a valid fcheap executable this exposes the
  # three core tools plus hitspec_capture_webpage.
  hitspec:
    command: /absolute/path/to/hitspec
    args: [mcp, serve, --workspace, /absolute/api-workspace, --search-provider, none, --fcheap-path, /absolute/path/to/fcheap]
    enabled: true
    description: Bounded HTTP retrieval, validation, and explicit durable capture
    tags: [http, api, markdown, artifacts]
    use_when:
      - fetch a public URL for bounded inspection without persistence
      - capture a reviewed public webpage as durable Markdown in file.cheap

  # Optional extension. The only injected value is the provider credential.
  hitspec_web:
    command: /absolute/path/to/hitspec
    args: [mcp, serve, --workspace, /absolute/api-workspace, --search-provider, tavily]
    vault: research
    vault_only: [TAVILY_API_KEY]
    enabled: true
    description: Protected live public-web discovery through Tavily
    tags: [web, research, discovery]
    use_when:
      - search the live public web and return normalized discovery candidates
```

The `--search-provider` and `--fcheap-path` flags are startup policy, not tool
arguments. When TinyVault is locked, `hitspec_web` fails closed before its MCP
server initializes, while the independently configured `hitspec` core can
remain connected. The protected process also exposes Hitspec's ordinary core
tools under its own `hitspec_web__...` namespace; route ordinary retrieval to
`hitspec` so it does not depend on the search credential.

This is an operational pattern, not a generic MCPHub feature: use it only when
the downstream server can safely run separate core and extension processes.
`vault_only` limits what TinyVault injects. Before starting the wrapper,
mcphub removes that selected name from its inherited and configured
environment, so a stale ambient `TAVILY_API_KEY` cannot shadow the vault.
TinyVault unlock variables are available only to the wrapper and are removed
from ordinary downstream processes. Do not export `TAVILY_API_KEY` or a
TinyVault passphrase globally.

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
      - hitspec_web # optional protected discovery process
```

If `servers` is absent, the agent already inherits every enabled server. Do not
create a `tools` allowlist just for this integration. If one already exists,
append whichever exact tools the agent needs while preserving its other
entries:

```yaml
tools:
  - hitspec__hitspec_fetch
  - hitspec__hitspec_capture_webpage
  - hitspec__hitspec_list_requests
  - hitspec__hitspec_validate
  - hitspec_web__hitspec_search_web
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

Hitspec always exposes three core tools and conditionally exposes two more.
mcphub prefixes their names with the configured server name:

| Hitspec tool | Namespaced name | Purpose |
| --- | --- | --- |
| `hitspec_fetch` | `hitspec__hitspec_fetch` | Fetch one direct URL or one saved request as `raw`, `text`, `markdown`, or `json`. |
| `hitspec_list_requests` | `hitspec__hitspec_list_requests` | List request names, methods, source lines, and tags in the fixed workspace. |
| `hitspec_validate` | `hitspec__hitspec_validate` | Parse and structurally validate one workspace file without executing it. |
| `hitspec_search_web` | `hitspec__hitspec_search_web` | Optional. Search through the server-configured provider and return bounded discovery candidates without persisting them. |
| `hitspec_capture_webpage` | `hitspec__hitspec_capture_webpage` | Optional. Fetch one public webpage, render Markdown, and persist it through the fixed file.cheap adapter. |

`hitspec_search_web` is present only when `--search-provider tavily` is
configured. In the split example it is named
`hitspec_web__hitspec_search_web`; the unprotected `hitspec` core deliberately
uses `--search-provider none`.
`hitspec_capture_webpage` is present only when `--fcheap-path` resolves to a
valid executable.

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

Every successful Hitspec tool call includes MCP text content. Webpage capture
also returns a typed compact stash receipt as structured content.

- `raw` returns a JSON text envelope with sanitized source, status, content
  type, byte size, `encoding: "base64"`, and the response data.
- `text` and `markdown` return the rendered document as text.
- `json` returns Hitspec's machine-safe response envelope serialized as text.
- list and validation results are JSON documents serialized as text.
- search returns bounded normalized discovery candidates and never raw provider
  metadata or page content.
- capture returns URL/status/size metadata plus a compact file.cheap receipt,
  not the captured page body.

HTTP `4xx` and `5xx` responses remain inspectable fetch results rather than MCP
transport failures. Machine-safe JSON provenance removes URL credentials,
query strings, fragments, and sensitive authorization or cookie headers.

If the serialized result exceeds mcphub's response budget, mcphub returns a
temporary `callId` receipt. Recover the exact result with
`mcphub_get_result`. That receipt is a transport safeguard with 24-hour
retention, not a file.cheap stash.

## Persist a reviewed response with file.cheap

When the fixed file.cheap adapter is configured, use the explicit capture tool
for a public webpage that should become durable Markdown:

```json
{
  "server": "hitspec",
  "tool": "hitspec_capture_webpage",
  "arguments": {
    "url": "https://example.com/guide",
    "name": "Example API guide",
    "tags": ["hitspec-response", "format:markdown"],
    "index": true
  }
}
```

Capture returns a compact stash receipt. It does not return the page body, and
its receipt keeps HTTP, save, and index outcomes distinct.

Register file.cheap as its own server when the agent also needs general artifact
storage, retrieval, or search:

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

For edited, combined, or otherwise transformed content, the manual handoff
remains deliberately explicit:

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
| `--search-provider` | `none` | Optional live-search provider (`none` or `tavily`). |
| `--fcheap-path` | unset | Optional fixed file.cheap executable used by durable webpage capture. |
| `--fcheap-stash-dir` | file.cheap default | Optional fixed stash root for capture. |

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

### Search or capture is missing

The three core tools are always present. `hitspec_search_web` requires a valid
`--search-provider tavily` and `hitspec_capture_webpage` requires a valid
`--fcheap-path`. In the split configuration, search belongs to `hitspec_web`;
its absence from the unprotected `hitspec` core is intentional. Check the
selected server's command-line flags, upgrade the binary if needed, and run
`mcphub doctor --server <server-name> --probe`.

### A vaulted server cannot initialize

Run `tvault status` and check whether its agent is available. A locked vault can
make `tvault run` exit before Hitspec starts. Current mcphub versions retain
only a bounded stderr tail, redact it, and reduce recognized failures to a
closed diagnostic such as `downstream stderr: vault is locked`; arbitrary
child stderr remains suppressed. Older versions may report only an MCP EOF.
Unlock interactively or start the TinyVault agent, then repeat the probe. Do
not solve this by exporting the passphrase globally. When core and protected
extension processes are registered separately, diagnose the failed protected
server while continuing to use the connected core; do not remove the vault
boundary merely to make search available.

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

`hitspec_fetch` never persists. Use `hitspec_capture_webpage` when the fixed
file.cheap adapter is configured and the original public webpage should be
stored as Markdown. For edited or composed content, recover the fetch result,
review it, create a trusted workspace file, then call `fcheap_save` explicitly.

### local-agent does not select Hitspec

Confirm the server is connected, the agent scope includes `hitspec`, and an
exact tool scope includes `hitspec__hitspec_fetch` when needed. Keep the tool
unpinned, describe the desired outcome in `use_when`, restart local-agent so it
receives current initialization instructions, and inspect the route with
`mcphub_resolve_tool`.
