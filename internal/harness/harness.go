// Package harness adapts mcphub's view of MCP servers into the on-disk config
// formats of the various agent harnesses:
//
//	claude   ~/.claude.json              JSON "mcpServers"
//	opencode opencode.json               JSON "mcp"
//	codex    ~/.codex/config.toml        TOML "[mcp_servers.*]"
//	crush    ~/.config/crush/crush.json  JSON "mcp"
//	forge    .mcp.json                   JSON "mcpServers" (entries use `disable`)
//	hermes   ~/.hermes/config.yaml       YAML "mcp_servers"
//	copilot  ~/.copilot/mcp-config.json  JSON "mcpServers" (type: local|http|sse)
//	qwen     ~/.qwen/settings.json       JSON "mcpServers" (httpUrl|url by transport)
//	gemini   ~/.gemini/settings.json     JSON "mcpServers" (httpUrl|url by transport)
//	kilo     ~/.config/kilo/kilo.jsonc   JSONC "mcp" (type: local|remote, command array)
//	kimi     ~/.kimi/config.toml         TOML "[mcp_servers.*]" (type: local|remote)
//	local-agent ~/.config/local-agent/config.yaml YAML "servers" sequence
//
// Each adapter is responsible for a SAFE read-modify-write: it produces a
// dry-run Plan (the diff) without writing, writes a timestamped .bak before
// mutating, and only rewrites the entries the diff actually changes — overlaying
// the modeled fields so any extra keys a user added to a managed entry (custom
// headers, timeouts, an explicit enabled:false) survive.
//
// The JSON adapters (claude/opencode/crush/forge/copilot/qwen/gemini/kilo)
// preserve every other key in the file byte-for-byte. readJSONObject strips
// JSONC comments so .jsonc files (kilo) parse the same as .json; comments are
// not preserved on write (a .bak is taken first). The Codex, Kimi (TOML), and
// Hermes (YAML) adapters are the other exception: they round-trip through a
// generic map, so on a write the whole file is reserialized — every key's
// VALUE is preserved, but comments and key/table ordering elsewhere are not.
// A .bak is always written first.
package harness

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// MCPServer is the format-neutral description of one server entry. Adapters
// translate this to/from their concrete file format.
type MCPServer struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Transport string // "http" | "sse" (remote only)
}

func (m MCPServer) isRemote() bool { return m.URL != "" }

// equal reports whether two server entries are materially the same (ignoring
// map/slice nil-vs-empty differences) so the planner can label unchanged rows.
func (m MCPServer) equal(o MCPServer) bool {
	if m.Command != o.Command || m.URL != o.URL || m.Transport != o.Transport {
		return false
	}
	if !reflect.DeepEqual(nonEmpty(m.Args), nonEmpty(o.Args)) {
		return false
	}
	return reflect.DeepEqual(nonEmptyMap(m.Env), nonEmptyMap(o.Env))
}

func nonEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nonEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// Action is the planned operation for a single server entry.
type Action string

const (
	ActionAdd       Action = "add"
	ActionUpdate    Action = "update"
	ActionRemove    Action = "remove"
	ActionUnchanged Action = "unchanged"
)

// Change is one entry in a Plan.
type Change struct {
	Server string `json:"server"`
	Action Action `json:"action"`
	// Detail explains an update field by field ("command \"mcphub\" →
	// \"/opt/homebrew/bin/mcphub\"; args [] → [mcp serve]") so a dry run is
	// reviewable without hand-diffing the harness file. Env VALUES are never
	// included — harness env blocks commonly hold credentials — only which
	// keys were added/removed/changed.
	Detail string `json:"detail,omitempty"`
}

// Plan is the set of changes an adapter would make (or made) to one file.
type Plan struct {
	PlanID  string   `json:"plan_id,omitempty"` // durable plan ID (SPEC §8.3) for resume/rollback
	Agent   string   `json:"agent"`
	Kind    string   `json:"kind"`
	Path    string   `json:"path"`
	Changes []Change `json:"changes"`
	Backup  string   `json:"backup,omitempty"`
	Applied bool     `json:"applied"`
}

// HasChanges reports whether the plan does anything beyond no-ops.
func (p Plan) HasChanges() bool {
	for _, c := range p.Changes {
		if c.Action != ActionUnchanged {
			return true
		}
	}
	return false
}

// Adapter reads and writes one harness config format.
type Adapter interface {
	// Kind is the adapter id matching config Agent.Type.
	Kind() string
	// List returns the MCP servers currently declared in the harness file,
	// sorted by name. A missing file yields an empty slice. Used by
	// `mcphub init --from-agents` to discover what an agent already has.
	List(path string) ([]MCPServer, error)
	// Apply makes the file contain exactly the `desired` managed servers
	// (alongside the user's own unmanaged entries), removing any server in
	// `owned` that is no longer desired. When dryRun is true nothing is written
	// and the returned Plan is the proposed diff.
	Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error)
}

// For returns the adapter for a harness type, or an error if unknown.
func For(kind string) (Adapter, error) {
	switch kind {
	case "claude", "claudecode":
		return claudeAdapter, nil
	case "opencode":
		return opencodeAdapter, nil
	case "codex":
		return codexAdapter{}, nil
	case "crush":
		return crushAdapter, nil
	case "forge", "forgecode":
		return forgeAdapter, nil
	case "hermes":
		return hermesAdapter{}, nil
	case "copilot":
		return copilotAdapter, nil
	case "qwen":
		return qwenAdapter, nil
	case "gemini":
		return geminiAdapter, nil
	case "kilo":
		return kiloAdapter, nil
	case "kimi":
		return kimiAdapter{}, nil
	case "local-agent", "localagent":
		return localAgentAdapter{}, nil
	default:
		return nil, fmt.Errorf("unknown harness type %q (supported: %s)", kind, strings.Join(Kinds(), ", "))
	}
}

// Kinds lists the supported harness types.
func Kinds() []string {
	return []string{"claude", "opencode", "codex", "crush", "forge", "hermes", "copilot", "qwen", "gemini", "kilo", "kimi", "local-agent"}
}

// DefaultPath returns the conventional config file path for a harness kind,
// or "" if the kind is unknown. Paths follow each tool's documented convention:
// home-dotfiles (claude, codex, copilot, qwen, gemini, kimi) and XDG-based
// (opencode, crush, kilo). The path uses ~ for home; callers expand it with
// config.ExpandPath before stat/open.
func DefaultPath(kind string) string {
	home, _ := os.UserHomeDir()
	xdg := xdgConfigHome()
	switch kind {
	case "claude":
		return filepath.Join(home, ".claude.json")
	case "opencode":
		return filepath.Join(xdg, "opencode", "opencode.json")
	case "codex":
		return filepath.Join(home, ".codex", "config.toml")
	case "crush":
		return filepath.Join(xdg, "crush", "crush.json")
	case "forge":
		return filepath.Join(home, "forge", ".mcp.json")
	case "hermes":
		return filepath.Join(home, ".hermes", "config.yaml")
	case "copilot":
		return filepath.Join(home, ".copilot", "mcp-config.json")
	case "qwen":
		return filepath.Join(home, ".qwen", "settings.json")
	case "gemini":
		return filepath.Join(home, ".gemini", "settings.json")
	case "kilo":
		return filepath.Join(xdg, "kilo", "kilo.jsonc")
	case "kimi":
		return filepath.Join(home, ".kimi", "config.toml")
	case "local-agent", "localagent":
		return filepath.Join(xdg, "local-agent", "config.yaml")
	}
	return ""
}

// xdgConfigHome returns $XDG_CONFIG_HOME, or ~/.config when unset.
func xdgConfigHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// remoteTransport returns the transport to record for a parsed entry. Transport
// is only meaningful for remote (url-based) servers; a local stdio entry — some
// harnesses tag these with type:"stdio" — carries no transport in mcphub's model.
func remoteTransport(url, typ string) string {
	if url == "" || typ == "stdio" {
		return ""
	}
	return typ
}

// sortedServers turns a name→server map into a name-sorted slice.
func sortedServers(m map[string]MCPServer) []MCPServer {
	out := make([]MCPServer, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// diff computes the change list to turn `existing` (the MCP entries currently
// in the file) into `desired`, removing `owned` entries no longer present.
// It is the shared core every adapter uses so the diff semantics are identical.
func diff(existing map[string]MCPServer, desired []MCPServer, owned []string) []Change {
	desiredByName := make(map[string]MCPServer, len(desired))
	for _, d := range desired {
		desiredByName[d.Name] = d
	}
	var changes []Change
	for _, d := range desired {
		cur, ok := existing[d.Name]
		switch {
		case !ok:
			changes = append(changes, Change{Server: d.Name, Action: ActionAdd})
		case cur.equal(d):
			changes = append(changes, Change{Server: d.Name, Action: ActionUnchanged})
		default:
			changes = append(changes, Change{Server: d.Name, Action: ActionUpdate, Detail: updateDetail(cur, d)})
		}
	}
	for _, name := range owned {
		if _, stillWanted := desiredByName[name]; !stillWanted {
			if _, present := existing[name]; present {
				changes = append(changes, Change{Server: name, Action: ActionRemove})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Server < changes[j].Server })
	return changes
}

// maxDetailBytes bounds a Change.Detail line so one pathological entry cannot
// flood a sync report.
const maxDetailBytes = 240

// updateDetail explains why an entry is planned as an update, field by field.
// Env VALUES are deliberately omitted (see Change.Detail), arg values that
// look like secrets are masked, and URLs are stripped of query/fragment/
// userinfo — a dry-run report must be safe to paste into an issue or CI log.
func updateDetail(cur, want MCPServer) string {
	quote := func(s string) string {
		if s == "" {
			return "(none)"
		}
		return fmt.Sprintf("%q", s)
	}
	var parts []string
	if cur.Command != want.Command {
		parts = append(parts, fmt.Sprintf("command %s → %s", quote(cur.Command), quote(want.Command)))
	}
	if !reflect.DeepEqual(nonEmpty(cur.Args), nonEmpty(want.Args)) {
		parts = append(parts, fmt.Sprintf("args %v → %v", redactArgs(cur.Args), redactArgs(want.Args)))
	}
	if cur.URL != want.URL {
		parts = append(parts, fmt.Sprintf("url %s → %s", quote(redactURL(cur.URL)), quote(redactURL(want.URL))))
	}
	if cur.Transport != want.Transport {
		parts = append(parts, fmt.Sprintf("transport %s → %s", quote(cur.Transport), quote(want.Transport)))
	}
	if envDiff := envKeyDiff(cur.Env, want.Env); envDiff != "" {
		parts = append(parts, "env keys "+envDiff)
	}
	return clampDetail(sanitizeDetail(strings.Join(parts, "; ")))
}

// secretFlagRe matches flag names whose VALUE must never appear in a report.
var secretFlagRe = regexp.MustCompile(`(?i)(token|secret|key|pass|auth|credential|bearer)`)

// redactArgs masks values that ride along in command arguments: "--token=xyz"
// becomes "--token=***", and the argument FOLLOWING a secret-named flag
// ("--api-key xyz") is masked too. Structural args (subcommands, --agent
// names) pass through — they are what the diff exists to show.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	maskNext := false
	for i, a := range args {
		switch {
		case maskNext:
			out[i] = "***"
			maskNext = false
		case strings.HasPrefix(a, "-") && strings.Contains(a, "="):
			name, _, _ := strings.Cut(a, "=")
			if secretFlagRe.MatchString(name) {
				out[i] = name + "=***"
			} else {
				out[i] = a
			}
		default:
			out[i] = a
			if strings.HasPrefix(a, "-") && secretFlagRe.MatchString(a) {
				maskNext = true
			}
		}
	}
	return out
}

// redactURL drops the query string, fragment, and userinfo — the parts of a
// URL that carry API keys — keeping scheme://host/path for identification.
func redactURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		if i := strings.IndexAny(raw, "?#"); i >= 0 {
			return raw[:i] + "?…"
		}
		return raw
	}
	changed := false
	if u.RawQuery != "" {
		u.RawQuery = "%E2%80%A6"
		changed = true
	}
	if u.Fragment != "" {
		u.Fragment = ""
		changed = true
	}
	if u.User != nil {
		u.User = url.User("***")
		changed = true
	}
	if !changed {
		return raw
	}
	return u.String()
}

// sanitizeDetail strips control characters (terminal escape sequences,
// carriage returns) so a hostile arg value cannot inject report lines or
// escape codes into the sync output.
func sanitizeDetail(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// clampDetail bounds the detail on a rune boundary so truncation never emits
// invalid UTF-8.
func clampDetail(detail string) string {
	if len(detail) <= maxDetailBytes {
		return detail
	}
	cut := maxDetailBytes
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut] + "…"
}

// envKeyDiff summarizes env drift as +added -removed ~changed KEY NAMES only.
func envKeyDiff(cur, want map[string]string) string {
	var marks []string
	for k, v := range want {
		if curV, ok := cur[k]; !ok {
			marks = append(marks, "+"+k)
		} else if curV != v {
			marks = append(marks, "~"+k)
		}
	}
	for k := range cur {
		if _, ok := want[k]; !ok {
			marks = append(marks, "-"+k)
		}
	}
	if len(marks) == 0 {
		return ""
	}
	sort.Strings(marks)
	return strings.Join(marks, " ")
}

// backup copies path to path.bak-<timestamp> before mutation. It is a no-op
// (returns "") when the file does not yet exist. The timestamp has only
// one-second resolution, so it never overwrites an existing backup — two syncs
// in the same second each get a unique `-N` suffix, preserving the true
// original.
func backup(path string) (string, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Preserve the source file's permissions (e.g. 0o600 for sensitive configs).
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	base := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102-150405"))
	dst := base
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.WriteFile(dst, body, mode); err != nil {
		return "", err
	}
	return dst, nil
}

// changedSet returns the names a plan would add or update (i.e. entries whose
// content actually changes), so adapters only rewrite those and leave every
// other managed entry — and any unmodeled keys on it — untouched.
func changedSet(plan Plan) map[string]bool {
	out := map[string]bool{}
	for _, c := range plan.Changes {
		if c.Action == ActionAdd || c.Action == ActionUpdate {
			out[c.Server] = true
		}
	}
	return out
}

// defaultHTTPTransport returns a copy of servers where a remote entry with no
// transport is set to "http". claude/crush persist the transport in a `type`
// field, defaulting an empty one to "http" on write; mirroring that default on
// the desired side before diffing keeps a remote server from being reported as
// changed on every sync.
func defaultHTTPTransport(servers []MCPServer) []MCPServer {
	out := make([]MCPServer, len(servers))
	copy(out, servers)
	for i := range out {
		if out[i].isRemote() && out[i].Transport == "" {
			out[i].Transport = "http"
		}
	}
	return out
}

// stripTransport returns a copy of servers with Transport cleared. opencode.json
// and codex's TOML can't represent http-vs-sse, so their parsers always read
// Transport back as "". Clearing it on the desired side before diffing keeps the
// comparison symmetric, so a remote server is not falsely reported as changed on
// every sync. The write path ignores Transport, so output is unaffected.
func stripTransport(servers []MCPServer) []MCPServer {
	out := make([]MCPServer, len(servers))
	copy(out, servers)
	for i := range out {
		out[i].Transport = ""
	}
	return out
}
