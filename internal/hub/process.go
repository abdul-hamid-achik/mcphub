package hub

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

const (
	maxDownstreamStderrBytes = 8 * 1024
	maxStartupDetailBytes    = 1024
)

var (
	tinyVaultCredentialNames = map[string]struct{}{
		"TVAULT_PASSPHRASE":   {},
		"TVAULT_IDENTITY_KEY": {},
		"TVAULT_AGENT_TOKEN":  {},
	}
	ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	secretNameRE = regexp.MustCompile(`(?i)(pass|token|secret|key|auth|credential|bearer|cookie|session|pat)`)
	// Match conventional KEY=value diagnostics without assuming a fixed
	// provider. The key name is retained because it is usually the actionable
	// part; only its value is removed.
	credentialAssignmentRE = regexp.MustCompile(`(?i)([A-Z][A-Z0-9_]*(?:PASS|TOKEN|SECRET|KEY|AUTH|CREDENTIAL|BEARER|COOKIE|SESSION|PAT)[A-Z0-9_]*\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	jsonCredentialRE       = regexp.MustCompile(`(?i)("(?:[^"]*(?:pass|token|secret|key|auth|credential|bearer|cookie|session|pat)[^"]*)"\s*:\s*)"[^"]*"`)
	bearerCredentialRE     = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]+`)
	authorizationHeaderRE  = regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*)(?:basic|bearer)\s+[^\s,;]+`)
	urlCredentialRE        = regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`)
	privateKeyBlockRE      = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	requiredEnvironmentRE  = regexp.MustCompile(`\b([A-Z][A-Z0-9_]{1,127})\s+(?:is\s+)?required\b`)
)

// preparedTransport carries startup diagnostics beside the SDK transport. The
// SDK intentionally exposes only the protocol connection, so mcphub retains a
// small stderr tail itself and consults it only when the handshake fails.
type preparedTransport struct {
	transport            mcp.Transport
	stderr               *boundedDiagnosticBuffer
	redactions           []string
	safeEnvironmentNames map[string]struct{}
}

func prepareTransport(srv config.Server) preparedTransport {
	if srv.IsRemote() {
		httpClient := httpClientFor(srv)
		switch srv.Transport {
		case "sse":
			return preparedTransport{transport: &mcp.SSEClientTransport{Endpoint: srv.URL, HTTPClient: httpClient}}
		default: // "http" or unset
			return preparedTransport{transport: &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: httpClient}}
		}
	}

	command, cargs := srv.SpawnCommand()
	cmd := exec.Command(command, cargs...)
	cmd.Env = serverEnvironment(srv, os.Environ())
	stderr := &boundedDiagnosticBuffer{limit: maxDownstreamStderrBytes}
	cmd.Stderr = stderr
	return preparedTransport{
		transport:            &mcp.CommandTransport{Command: cmd},
		stderr:               stderr,
		redactions:           sensitiveEnvironmentValues(cmd.Env),
		safeEnvironmentNames: startupEnvironmentAllowlist(srv),
	}
}

func (p preparedTransport) startupDetail() string {
	if p.stderr == nil {
		return ""
	}
	return sanitizeStartupDetail(
		p.stderr.String(),
		p.redactions,
		p.safeEnvironmentNames,
		p.stderr.Truncated(),
	)
}

// serverEnvironment keeps explicit server env overrides deterministic and
// prevents TinyVault unlock credentials from reaching unrelated processes.
// A tvault wrapper (or a directly configured tvault MCP server) retains those
// credentials long enough to unlock; TinyVault is responsible for removing
// them before it execs its own child.
func serverEnvironment(srv config.Server, inherited []string) []string {
	env := mergeEnvironment(inherited, srv.Env)
	if srv.UsesVault() {
		// A selected vault value must come from TinyVault, not from a stale
		// ambient/exported value with the same name. This also avoids duplicate
		// entries whose precedence differs across child runtimes.
		return filterEnvironment(env, func(name string) bool {
			if isTinyVaultRuntimeVariable(name) {
				return true
			}
			if vaultSelectsEnvironmentName(srv, name) {
				return false
			}
			return true
		})
	}
	if filepath.Base(srv.Command) == "tvault" {
		return env
	}
	return filterEnvironment(env, func(name string) bool {
		return !isTinyVaultCredential(name)
	})
}

func mergeEnvironment(inherited []string, explicit map[string]string) []string {
	overridden := make(map[string]struct{}, len(explicit))
	for name := range explicit {
		overridden[name] = struct{}{}
	}

	seen := make(map[string]struct{}, len(inherited)+len(explicit))
	out := make([]string, 0, len(inherited)+len(explicit))
	for _, entry := range inherited {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		if _, skip := overridden[name]; skip {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, entry)
	}

	names := make([]string, 0, len(explicit))
	for name := range explicit {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, name+"="+explicit[name])
	}
	return out
}

func filterEnvironment(env []string, keep func(name string) bool) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && keep(name) {
			out = append(out, entry)
		}
	}
	return out
}

func isTinyVaultCredential(name string) bool {
	_, ok := tinyVaultCredentialNames[strings.ToUpper(name)]
	return ok
}

func isTinyVaultRuntimeVariable(name string) bool {
	if isTinyVaultCredential(name) {
		return true
	}
	switch strings.ToUpper(name) {
	case "TVAULT_DIR", "TVAULT_NO_AGENT", "TVAULT_IDENTITY":
		return true
	default:
		return false
	}
}

func vaultSelectsEnvironmentName(srv config.Server, name string) bool {
	for _, selected := range srv.VaultOnly {
		if name == selected {
			return true
		}
	}
	return srv.VaultPrefix != "" && strings.HasPrefix(name, srv.VaultPrefix)
}

func sensitiveEnvironmentValues(env []string) []string {
	var values []string
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" {
			continue
		}
		if isTinyVaultCredential(name) || secretNameRE.MatchString(name) {
			values = append(values, value)
		}
	}
	// Replace longer values first so a short value cannot leave a suffix of a
	// longer credential behind.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

// startupEnvironmentAllowlist returns environment-variable identifiers that
// are safe to repeat in a diagnostic. They come only from explicit config keys
// or TinyVault's closed runtime contract; an arbitrary identifier printed by a
// child is never reflected back to the caller.
func startupEnvironmentAllowlist(srv config.Server) map[string]struct{} {
	names := make(map[string]struct{}, len(srv.Env)+len(srv.VaultOnly)+6)
	for name := range srv.Env {
		names[strings.ToUpper(name)] = struct{}{}
	}
	for _, name := range srv.VaultOnly {
		names[strings.ToUpper(name)] = struct{}{}
	}
	for name := range tinyVaultCredentialNames {
		names[name] = struct{}{}
	}
	for _, name := range []string{"TVAULT_DIR", "TVAULT_NO_AGENT", "TVAULT_IDENTITY"} {
		names[name] = struct{}{}
	}
	return names
}

// boundedDiagnosticBuffer retains only the most recent bytes. Startup errors
// conventionally appear at the end, and retaining a tail prevents verbose
// child logs from crowding out the useful line.
type boundedDiagnosticBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

func (b *boundedDiagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 || b.limit <= 0 {
		return n, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		b.truncated = true
		return n, nil
	}
	if len(p) == b.limit {
		if len(b.buf) > 0 {
			b.truncated = true
		}
		b.buf = append(b.buf[:0], p...)
		return n, nil
	}
	overflow := len(b.buf) + len(p) - b.limit
	if overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *boundedDiagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.buf...))
}

func (b *boundedDiagnosticBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func sanitizeStartupDetail(raw string, redactions []string, safeEnvironmentNames map[string]struct{}, truncated bool) string {
	if raw == "" {
		return ""
	}
	detail := strings.ToValidUTF8(raw, "\uFFFD")
	for _, value := range redactions {
		detail = strings.ReplaceAll(detail, value, "[REDACTED]")
	}
	detail = ansiEscapeRE.ReplaceAllString(detail, "")
	detail = privateKeyBlockRE.ReplaceAllString(detail, "[REDACTED PRIVATE KEY]")
	detail = jsonCredentialRE.ReplaceAllString(detail, `${1}"[REDACTED]"`)
	detail = credentialAssignmentRE.ReplaceAllString(detail, `${1}[REDACTED]`)
	detail = authorizationHeaderRE.ReplaceAllString(detail, `${1}[REDACTED]`)
	detail = bearerCredentialRE.ReplaceAllString(detail, `${1}[REDACTED]`)
	detail = urlCredentialRE.ReplaceAllString(detail, `${1}[REDACTED]@`)
	detail = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, detail)
	detail = strings.Join(strings.Fields(detail), " ")
	if detail == "" {
		return ""
	}
	// Child stderr is untrusted and may contain a vault-injected value that
	// mcphub has never seen, so regex redaction alone cannot prove arbitrary
	// prose safe. Emit only fixed summaries and identifiers from closed,
	// actionable startup-error classes; suppress every unknown diagnostic.
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "vault is locked") || strings.Contains(lower, `"vault_locked"`):
		detail = "vault is locked; start a TinyVault agent or provide unlock credentials to the launcher"
	case requiredEnvironmentRE.MatchString(detail):
		match := requiredEnvironmentRE.FindStringSubmatch(detail)
		if _, safe := safeEnvironmentNames[strings.ToUpper(match[1])]; safe {
			detail = "required environment variable " + match[1] + " is unavailable"
		} else {
			detail = "a required environment variable is unavailable"
		}
	case strings.Contains(lower, "permission denied"):
		detail = "downstream reported permission denied during startup"
	case strings.Contains(lower, "no such file or directory"):
		detail = "downstream reported a missing file during startup"
	case strings.Contains(lower, "unknown flag") || strings.Contains(lower, "unrecognized option"):
		detail = "downstream rejected a startup option"
	default:
		detail = "downstream emitted startup diagnostics; detail suppressed"
	}
	if truncated {
		detail += " (stderr exceeded capture limit)"
	}
	if len(detail) <= maxStartupDetailBytes {
		return detail
	}
	cut := maxStartupDetailBytes - len("…")
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	if cut <= 0 {
		return ""
	}
	return detail[:cut] + "…"
}

var _ io.Writer = (*boundedDiagnosticBuffer)(nil)
