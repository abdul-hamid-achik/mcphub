package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

var gateway = []MCPServer{{Name: "mcphub", Command: "/bin/mcphub", Args: []string{"mcp", "serve"}}}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// applyLifecycle exercises add → dry-run safety → write → idempotency →
// removal for any adapter, asserting the user's own keys survive.
func applyLifecycle(t *testing.T, a Adapter, path, mustPreserve string) {
	t.Helper()

	// dry run must not change the file
	before := read(t, path)
	plan, err := a.Apply(path, gateway, nil, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !plan.HasChanges() {
		t.Fatal("expected an add change")
	}
	if read(t, path) != before {
		t.Fatal("dry run mutated the file")
	}

	// write
	plan, err = a.Apply(path, gateway, nil, false)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !plan.Applied {
		t.Fatal("plan should be applied")
	}
	body := read(t, path)
	if !strings.Contains(body, "mcphub") {
		t.Errorf("mcphub not written: %s", body)
	}
	if mustPreserve != "" && !strings.Contains(body, mustPreserve) {
		t.Errorf("lost pre-existing %q: %s", mustPreserve, body)
	}

	// idempotent: re-applying the same desired+owned is a no-op
	plan, _ = a.Apply(path, gateway, []string{"mcphub"}, false)
	if plan.HasChanges() {
		t.Errorf("second apply should be up to date, got %+v", plan.Changes)
	}

	// removal: mcphub is owned but no longer desired
	plan, _ = a.Apply(path, nil, []string{"mcphub"}, false)
	removed := false
	for _, ch := range plan.Changes {
		if ch.Server == "mcphub" && ch.Action == ActionRemove {
			removed = true
		}
	}
	if !removed {
		t.Errorf("expected mcphub removal, got %+v", plan.Changes)
	}
	if strings.Contains(read(t, path), "mcphub") {
		t.Error("mcphub should be gone after removal")
	}
}

func TestClaudeLifecycle(t *testing.T) {
	p := writeFile(t, "claude.json", `{"mcpServers":{"keep":{"command":"k"}},"numFlag":42}`)
	applyLifecycle(t, claudeAdapter, p, "numFlag")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written server should survive")
	}
}

func TestOpencodeLifecycle(t *testing.T) {
	p := writeFile(t, "opencode.json", `{"$schema":"x","mcp":{},"model":"a/b"}`)
	applyLifecycle(t, opencodeAdapter, p, "model")
}

func TestCodexLifecycle(t *testing.T) {
	p := writeFile(t, "config.toml", "model = \"gpt\"\n[mcp_servers.existing]\ncommand = \"keepme\"\n")
	applyLifecycle(t, codexAdapter{}, p, "keepme")
}

func TestCrushLifecycle(t *testing.T) {
	p := writeFile(t, "crush.json", `{"$schema":"x","mcp":{"keep":{"command":"k","type":"stdio"}},"options":{"a":1}}`)
	applyLifecycle(t, crushAdapter, p, "options")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written crush server should survive")
	}
}

func TestListReadsLocalAndRemote(t *testing.T) {
	// type:"stdio" must NOT become a transport; only a url-based remote does.
	p := writeFile(t, "claude.json", `{"mcpServers":{
		"local":{"command":"c","args":["x"],"type":"stdio"},
		"remote":{"type":"http","url":"https://e.com"}}}`)
	got, err := claudeAdapter.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %d servers, want 2", len(got))
	}
	byName := map[string]MCPServer{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if byName["local"].Transport != "" {
		t.Errorf("local stdio should have empty transport, got %q", byName["local"].Transport)
	}
	if byName["remote"].Transport != "http" || byName["remote"].URL != "https://e.com" {
		t.Errorf("remote parsed wrong: %+v", byName["remote"])
	}
}

func TestListMissingFileIsEmpty(t *testing.T) {
	got, err := claudeAdapter.List(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || len(got) != 0 {
		t.Errorf("missing file should be empty, got %v err %v", got, err)
	}
}

func TestDiffTransitions(t *testing.T) {
	existing := map[string]MCPServer{
		"a":            {Name: "a", Command: "x"},
		"b":            {Name: "b", Command: "old"},
		"gone_present": {Name: "gone_present", Command: "z"},
	}
	desired := []MCPServer{
		{Name: "a", Command: "x"},   // unchanged
		{Name: "b", Command: "new"}, // update
		{Name: "c", Command: "y"},   // add
	}
	owned := []string{"b", "gone_present", "gone_absent"} // gone_absent not in file
	got := diff(existing, desired, owned)
	want := []Change{
		{Server: "a", Action: ActionUnchanged},
		{Server: "b", Action: ActionUpdate, Detail: `command "old" → "new"`},
		{Server: "c", Action: ActionAdd},
		{Server: "gone_present", Action: ActionRemove},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff mismatch:\n got %+v\nwant %+v", got, want)
	}
	for _, ch := range got {
		if ch.Server == "gone_absent" {
			t.Error("gone_absent (already absent) must not be reported as a remove")
		}
	}
}

func TestEqualNilVsEmpty(t *testing.T) {
	a := MCPServer{Name: "s", Command: "c"}
	b := MCPServer{Name: "s", Command: "c", Args: []string{}, Env: map[string]string{}}
	if !a.equal(b) {
		t.Error("nil and empty Args/Env should compare equal")
	}
}

// TestRemoteIdempotent pins finding-2: a second sync of an unchanged remote
// server must be a no-op (no churn, no fresh .bak) for every adapter.
func TestRemoteIdempotent(t *testing.T) {
	cases := []struct {
		name       string
		a          Adapter
		file, seed string
	}{
		{"claude", claudeAdapter, "claude.json", `{"mcpServers":{}}`},
		{"crush", crushAdapter, "crush.json", `{"mcp":{}}`},
		{"opencode", opencodeAdapter, "opencode.json", `{"mcp":{}}`},
		{"codex", codexAdapter{}, "config.toml", ""},
		{"copilot", copilotAdapter, "copilot.json", `{"mcpServers":{}}`},
		{"qwen", qwenAdapter, "qwen.json", `{"mcpServers":{}}`},
		{"gemini", geminiAdapter, "gemini.json", `{"mcpServers":{}}`},
		{"kilo", kiloAdapter, "kilo.jsonc", `{"mcp":{}}`},
		{"kimi", kimiAdapter{}, "kimi.toml", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, transport := range []string{"sse", ""} {
				p := writeFile(t, tc.file, tc.seed)
				remote := []MCPServer{{Name: "r", URL: "https://e.com", Transport: transport}}
				if _, err := tc.a.Apply(p, remote, nil, false); err != nil {
					t.Fatalf("first apply: %v", err)
				}
				plan, err := tc.a.Apply(p, remote, []string{"r"}, true)
				if err != nil {
					t.Fatalf("second apply: %v", err)
				}
				if plan.HasChanges() {
					t.Errorf("transport=%q: second apply not idempotent: %+v", transport, plan.Changes)
				}
			}
		})
	}
}

// TestClaudePreservesExtraKeys pins finding-1: a write that changes a sibling
// must not strip user-added keys (e.g. an auth header) off an unchanged entry.
func TestClaudePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "claude.json",
		`{"mcpServers":{"api":{"type":"http","url":"https://api.x","headers":{"Authorization":"Bearer SECRET"}}}}`)
	a := claudeAdapter
	desired := []MCPServer{
		{Name: "api", URL: "https://api.x", Transport: "http"}, // unchanged
		{Name: "new", Command: "x", Args: []string{"y"}},       // sibling add
	}
	plan, err := a.Apply(p, desired, []string{"api"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range plan.Changes {
		if ch.Server == "api" && ch.Action != ActionUnchanged {
			t.Errorf("api should be unchanged, got %s", ch.Action)
		}
	}
	body := read(t, p)
	if !strings.Contains(body, "Bearer SECRET") {
		t.Errorf("sibling write dropped api's auth header:\n%s", body)
	}
	if !strings.Contains(body, `"new"`) {
		t.Error("sibling server was not added")
	}
}

// TestUpdatePreservesExtraKeys pins finding-1 on the update path: updating an
// entry overlays modeled fields but keeps unmodeled ones.
func TestUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "claude.json",
		`{"mcpServers":{"api":{"type":"http","url":"https://old.x","timeout":30,"headers":{"X":"Y"}}}}`)
	a := claudeAdapter
	desired := []MCPServer{{Name: "api", URL: "https://new.x", Transport: "http"}} // url changed => update
	if _, err := a.Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "https://new.x") {
		t.Error("url should have updated")
	}
	if !strings.Contains(body, `"timeout"`) || !strings.Contains(body, `"X"`) {
		t.Errorf("update dropped unmodeled keys (timeout/headers):\n%s", body)
	}
}

// TestMalformedManagedEntryNotDeleted pins finding-22: an owned entry that the
// parser can't read (so the dry-run plan never mentions it) must not be silently
// deleted on a write triggered by a sibling change.
func TestMalformedManagedEntryNotDeleted(t *testing.T) {
	p := writeFile(t, "claude.json", `{"mcpServers":{"bad":12345,"keep":{"command":"k"}}}`)
	a := claudeAdapter
	desired := []MCPServer{{Name: "keep", Command: "k"}, {Name: "new", Command: "n"}}
	plan, err := a.Apply(p, desired, []string{"bad"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range plan.Changes {
		if ch.Server == "bad" {
			t.Errorf("malformed 'bad' should not appear in the plan: %+v", ch)
		}
	}
	if !strings.Contains(read(t, p), "12345") {
		t.Error("malformed managed entry was silently deleted")
	}
}

func TestForgeLifecycle(t *testing.T) {
	p := writeFile(t, "forge.mcp.json", `{"mcpServers":{"keep":{"command":"k","disable":false}}}`)
	applyLifecycle(t, forgeAdapter, p, "keep")

	// a freshly written entry does NOT carry disable (it's user-owned, omitempty)
	p2 := writeFile(t, "forge2.mcp.json", `{"mcpServers":{}}`)
	a := forgeAdapter
	if _, err := a.Apply(p2, gateway, nil, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, p2), `"disable"`) {
		t.Errorf("fresh forge entry should not carry disable (user-owned):\n%s", read(t, p2))
	}
}

func TestHermesLifecycle(t *testing.T) {
	p := writeFile(t, "hermes.yaml", "max_tokens: 100\nmcp_servers:\n  keep:\n    command: k\n    enabled: true\nother_top: val\n")
	applyLifecycle(t, hermesAdapter{}, p, "other_top")
	if !strings.Contains(read(t, p), "max_tokens") {
		t.Error("hermes adapter dropped an unrelated top-level key")
	}
}

func TestForUnknownKind(t *testing.T) {
	if _, err := For("nope"); err == nil {
		t.Error("expected error for unknown harness")
	}
	for _, k := range Kinds() {
		if _, err := For(k); err != nil {
			t.Errorf("For(%q) failed: %v", k, err)
		}
	}
}

func TestOpencodeFlattensCommand(t *testing.T) {
	p := writeFile(t, "opencode.json", `{"mcp":{}}`)
	desired := []MCPServer{{Name: "cm", Command: "codemap", Args: []string{"serve"}}}
	a := opencodeAdapter
	if _, err := a.Apply(p, desired, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, `"codemap"`) || !strings.Contains(body, `"serve"`) {
		t.Errorf("command+args should flatten into one array: %s", body)
	}
}

func TestCopilotLifecycle(t *testing.T) {
	p := writeFile(t, "mcp-config.json", `{"mcpServers":{"keep":{"type":"stdio","command":"k"}},"tools":{}}`)
	applyLifecycle(t, copilotAdapter, p, "tools")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written copilot server should survive")
	}
	// a freshly written stdio entry carries type:"stdio"
	p2 := writeFile(t, "copilot2.json", `{"mcpServers":{}}`)
	if _, err := copilotAdapter.Apply(p2, gateway, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, p2), `"type": "stdio"`) {
		t.Errorf("copilot stdio entry should carry type:stdio:\n%s", read(t, p2))
	}
}

func TestQwenLifecycle(t *testing.T) {
	p := writeFile(t, "settings.json", `{"mcpServers":{"keep":{"command":"k"}},"model":"qwen"}`)
	applyLifecycle(t, qwenAdapter, p, "model")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written qwen server should survive")
	}
	// remote http → httpUrl
	p2 := writeFile(t, "qwen2.json", `{"mcpServers":{}}`)
	remote := []MCPServer{{Name: "r", URL: "https://e.com", Transport: "http"}}
	if _, err := qwenAdapter.Apply(p2, remote, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, p2), `"httpUrl"`) {
		t.Errorf("qwen http remote should use httpUrl:\n%s", read(t, p2))
	}
	// remote sse → url
	p3 := writeFile(t, "qwen3.json", `{"mcpServers":{}}`)
	sse := []MCPServer{{Name: "r", URL: "https://e.com", Transport: "sse"}}
	if _, err := qwenAdapter.Apply(p3, sse, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, p3), `"url"`) || strings.Contains(read(t, p3), "httpUrl") {
		t.Errorf("qwen sse remote should use url not httpUrl:\n%s", read(t, p3))
	}
}

func TestGeminiLifecycle(t *testing.T) {
	p := writeFile(t, "gemini.json", `{"mcpServers":{"keep":{"command":"k"}},"ui":{"theme":"dark"}}`)
	applyLifecycle(t, geminiAdapter, p, "theme")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written gemini server should survive")
	}
	// remote http → httpUrl (same as qwen)
	p2 := writeFile(t, "gemini2.json", `{"mcpServers":{}}`)
	remote := []MCPServer{{Name: "r", URL: "https://e.com", Transport: "http"}}
	if _, err := geminiAdapter.Apply(p2, remote, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, p2), `"httpUrl"`) {
		t.Errorf("gemini http remote should use httpUrl:\n%s", read(t, p2))
	}
}

func TestKiloLifecycle(t *testing.T) {
	// kilo uses JSONC — comments must not break parsing
	p := writeFile(t, "kilo.jsonc", `// top comment
{
  "mcp": {
    "keep": {"type": "local", "command": ["k"], "enabled": true}
  },
  "other": 42 /* inline */
}`)
	applyLifecycle(t, kiloAdapter, p, "other")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written kilo server should survive")
	}
	// freshly written local entry flattens command+args and uses "environment"
	p2 := writeFile(t, "kilo2.jsonc", `{"mcp":{}}`)
	desired := []MCPServer{{Name: "cm", Command: "codemap", Args: []string{"serve"}, Env: map[string]string{"X": "1"}}}
	if _, err := kiloAdapter.Apply(p2, desired, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p2)
	if !strings.Contains(body, `"codemap"`) || !strings.Contains(body, `"serve"`) {
		t.Errorf("kilo should flatten command+args: %s", body)
	}
	if !strings.Contains(body, `"environment"`) {
		t.Errorf("kilo should use environment not env: %s", body)
	}
}

func TestKimiLifecycle(t *testing.T) {
	p := writeFile(t, "config.toml", "default_model = \"kimi\"\n[mcp_servers.existing]\ntype = \"local\"\ncommand = [\"keepme\"]\nenabled = true\n")
	applyLifecycle(t, kimiAdapter{}, p, "keepme")
	if !strings.Contains(read(t, p), "default_model") {
		t.Error("kimi adapter dropped an unrelated top-level key")
	}
	// freshly written local entry carries type:"local" but NOT enabled (user-owned)
	p2 := writeFile(t, "kimi2.toml", "")
	desired := []MCPServer{{Name: "cm", Command: "codemap", Args: []string{"serve"}}}
	if _, err := (kimiAdapter{}).Apply(p2, desired, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p2)
	if !strings.Contains(body, `'local'`) {
		t.Errorf("kimi entry should carry type:local:\n%s", body)
	}
	if strings.Contains(body, "enabled") {
		t.Errorf("kimi entry should NOT carry enabled (user-owned):\n%s", body)
	}
}

func TestStripJSONComments(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"no comments", `{"a":1}`, `{"a":1}`},
		{"line comment", `{"a":1}\n// trailing`, `{"a":1}\n`},
		{"block comment", `{"a":/* x */1}`, `{"a":1}`},
		{"url in string", `{"u":"https://x.com"}`, `{"u":"https://x.com"}`},
		{"escaped quote", `{"u":"he said \"hi\""}`, `{"u":"he said \"hi\""}`},
		{"comment in string", `{"u":"// not a comment"}`, `{"u":"// not a comment"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(stripJSONComments([]byte(tc.in)))
			// normalize: trim all whitespace for comparison
			if strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(tc.want, " ", "") {
				t.Errorf("strip(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestKimiUpdatePreservesExtraKeys pins the kimi overlay fix: updating a kimi
// entry must overlay managed keys but keep unmodeled ones (e.g. tools/approval).
func TestKimiUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "kimi_update.toml",
		`[mcp_servers.api]
type = 'local'
command = ['old']
enabled = true
tools = ['a', 'b']
`)
	a := kimiAdapter{}
	desired := []MCPServer{{Name: "api", Command: "new"}} // command changed -> update
	if _, err := a.Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "new") {
		t.Error("command should have updated to 'new'")
	}
	if !strings.Contains(body, "'a'") || !strings.Contains(body, "'b'") {
		t.Errorf("unmodeled tools key should survive update:\n%s", body)
	}
	if !strings.Contains(body, `type = 'local'`) {
		t.Errorf("type should survive overlay:\n%s", body)
	}
	if !strings.Contains(body, "enabled = true") {
		t.Errorf("enabled should survive overlay:\n%s", body)
	}
}

// TestGeminiSseUsesUrl closes the asymmetry with qwen: sse transport must write
// `url`, not `httpUrl`.
func TestGeminiSseUsesUrl(t *testing.T) {
	p := writeFile(t, "gemini_sse.json", `{"mcpServers":{}}`)
	sse := []MCPServer{{Name: "r", URL: "https://e.com", Transport: "sse"}}
	if _, err := geminiAdapter.Apply(p, sse, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, `"url"`) {
		t.Errorf("gemini sse should use url:\n%s", body)
	}
	if strings.Contains(body, "httpUrl") {
		t.Errorf("gemini sse should NOT use httpUrl:\n%s", body)
	}
}

// TestQwenGeminiParseTransportField guards the READ direction: httpUrl -> http,
// url -> sse, httpUrl takes precedence when both present.
func TestQwenGeminiParseTransportField(t *testing.T) {
	adapters := []struct {
		name string
		a    jsonAdapter
	}{
		{"qwen", qwenAdapter},
		{"gemini", geminiAdapter},
	}
	for _, tc := range adapters {
		t.Run(tc.name, func(t *testing.T) {
			// httpUrl -> http
			p := writeFile(t, tc.name+"_http.json", `{"mcpServers":{"r":{"httpUrl":"https://h.com"}}}`)
			got, err := tc.a.List(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].URL != "https://h.com" || got[0].Transport != "http" {
				t.Errorf("httpUrl parse: got %+v", got)
			}
			// url -> sse
			p = writeFile(t, tc.name+"_sse.json", `{"mcpServers":{"r":{"url":"https://s.com"}}}`)
			got, err = tc.a.List(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].URL != "https://s.com" || got[0].Transport != "sse" {
				t.Errorf("url parse: got %+v", got)
			}
			// both present -> httpUrl wins
			p = writeFile(t, tc.name+"_both.json", `{"mcpServers":{"r":{"httpUrl":"https://h.com","url":"https://s.com"}}}`)
			got, err = tc.a.List(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].URL != "https://h.com" || got[0].Transport != "http" {
				t.Errorf("precedence: httpUrl should win, got %+v", got)
			}
			// stdio: command+args, no transport
			p = writeFile(t, tc.name+"_local.json", `{"mcpServers":{"r":{"command":"npx","args":["-y","x"]}}}`)
			got, err = tc.a.List(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Command != "npx" || len(got[0].Args) != 2 || got[0].Transport != "" {
				t.Errorf("stdio parse: got %+v", got)
			}
		})
	}
}

// TestCopilotRemoteTypeAndReadback verifies copilot writes type:http/sse for
// remotes and round-trips them through List.
func TestCopilotRemoteTypeAndReadback(t *testing.T) {
	for _, tc := range []struct {
		transport, wantType string
	}{
		{"http", "http"},
		{"sse", "sse"},
		{"", "http"}, // empty defaults to http
	} {
		t.Run(tc.transport, func(t *testing.T) {
			p := writeFile(t, "copilot_"+tc.transport+".json", `{"mcpServers":{}}`)
			remote := []MCPServer{{Name: "r", URL: "https://e.com", Transport: tc.transport}}
			if _, err := copilotAdapter.Apply(p, remote, nil, false); err != nil {
				t.Fatal(err)
			}
			body := read(t, p)
			if !strings.Contains(body, `"type": "`+tc.wantType+`"`) {
				t.Errorf("expected type %q:\n%s", tc.wantType, body)
			}
			// read back
			got, err := copilotAdapter.List(p)
			if err != nil {
				t.Fatal(err)
			}
			wantTransport := tc.transport
			if wantTransport == "" {
				wantTransport = "http"
			}
			if len(got) != 1 || got[0].URL != "https://e.com" || got[0].Transport != wantTransport {
				t.Errorf("read-back: got %+v", got)
			}
		})
	}
}

// TestCopilotUpdatePreservesExtraKeys verifies that updating a copilot entry
// keeps unmodeled per-entry keys (headers, timeout, tools).
func TestCopilotUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "copilot_update.json",
		`{"mcpServers":{"api":{"type":"http","url":"https://old","headers":{"Authorization":"Bearer X"},"timeout":30}}}`)
	desired := []MCPServer{{Name: "api", URL: "https://new", Transport: "http"}}
	if _, err := copilotAdapter.Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "https://new") {
		t.Error("url should have updated")
	}
	if !strings.Contains(body, "Bearer X") || !strings.Contains(body, `"timeout"`) {
		t.Errorf("update dropped unmodeled keys (headers/timeout):\n%s", body)
	}
}

// TestKiloRemoteAndFreshFlags tests the remote branch and the enabled/type
// flags on a fresh local write.
func TestKiloRemoteAndFreshFlags(t *testing.T) {
	// remote -> type:remote, url, no command, no enabled (user-owned)
	p := writeFile(t, "kilo_remote.jsonc", `{"mcp":{}}`)
	remote := []MCPServer{{Name: "r", URL: "https://e.com", Transport: "http"}}
	if _, err := kiloAdapter.Apply(p, remote, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, `"remote"`) || !strings.Contains(body, `"https://e.com"`) {
		t.Errorf("kilo remote: missing type/url:\n%s", body)
	}
	if strings.Contains(body, `"command"`) {
		t.Errorf("kilo remote: should NOT have command:\n%s", body)
	}
	// local -> type:local, no enabled (user-owned)
	p = writeFile(t, "kilo_local.jsonc", `{"mcp":{}}`)
	local := []MCPServer{{Name: "cm", Command: "codemap", Args: []string{"serve"}}}
	if _, err := kiloAdapter.Apply(p, local, nil, false); err != nil {
		t.Fatal(err)
	}
	body = read(t, p)
	if !strings.Contains(body, `"local"`) {
		t.Errorf("kilo local: missing type:\n%s", body)
	}
	// read-back: transport is stripped (kilo can't represent http vs sse)
	got, err := kiloAdapter.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "codemap" || len(got[0].Args) != 1 || got[0].Transport != "" {
		t.Errorf("kilo read-back: got %+v", got)
	}
}

// TestKimiRemoteAndEnvironment tests the remote branch and env->environment
// write + read-back for kimi.
func TestKimiRemoteAndEnvironment(t *testing.T) {
	// remote
	p := writeFile(t, "kimi_remote.toml", "")
	remote := []MCPServer{{Name: "r", URL: "https://e.com"}}
	if _, err := (kimiAdapter{}).Apply(p, remote, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, `remote`) || !strings.Contains(body, `https://e.com`) {
		t.Errorf("kimi remote: missing type/url:\n%s", body)
	}
	// local with env -> environment table
	p = writeFile(t, "kimi_env.toml", "")
	local := []MCPServer{{Name: "cm", Command: "codemap", Args: []string{"serve"}, Env: map[string]string{"X": "1"}}}
	if _, err := (kimiAdapter{}).Apply(p, local, nil, false); err != nil {
		t.Fatal(err)
	}
	body = read(t, p)
	if !strings.Contains(body, "environment") || !strings.Contains(body, "X") {
		t.Errorf("kimi env: should write under 'environment':\n%s", body)
	}
	// read-back
	got, err := kimiAdapter{}.List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "codemap" || len(got[0].Args) != 1 || got[0].Env["X"] != "1" {
		t.Errorf("kimi read-back: got %+v", got)
	}
}

// TestForReturnsCorrectKind verifies adapter.Kind() matches the requested kind.
func TestForReturnsCorrectKind(t *testing.T) {
	for _, k := range Kinds() {
		a, err := For(k)
		if err != nil {
			t.Errorf("For(%q): %v", k, err)
			continue
		}
		if a.Kind() != k {
			t.Errorf("For(%q).Kind() = %q", k, a.Kind())
		}
	}
}

// TestKiloJSONCEdgeComments exercises JSONC comments inside the mcp object
// and inside a string value (not a comment).
func TestKiloJSONCEdgeComments(t *testing.T) {
	p := writeFile(t, "kilo_edge.jsonc", `{
  // top comment
  "mcp": {
    "keep": {  // inline comment
      "type": "local",
      "command": ["k"]
    }
  },
  "note": "url with // is not a comment"
}`)
	applyLifecycle(t, kiloAdapter, p, "note")
	// the string value with // must survive
	if !strings.Contains(read(t, p), "not a comment") {
		t.Error("string value containing // was corrupted")
	}
}

// TestUpdatePreservesExtraKeysAllAdapters pins finding-1 on the UPDATE path
// for every JSON adapter: updating an entry overlays modeled fields but keeps
// unmodeled ones. claude and copilot already have dedicated tests above; this
// covers opencode, crush, forge, and kilo.
func TestUpdatePreservesExtraKeysAllAdapters(t *testing.T) {
	cases := []struct {
		name    string
		a       Adapter
		file    string
		seed    string
		desired []MCPServer
		owned   []string
		// A string that must survive the update (an unmodeled key's value).
		mustSurvive string
		// A string confirming the modeled field was updated.
		mustUpdate string
	}{
		{
			name:        "opencode",
			a:           opencodeAdapter,
			file:        "oc.json",
			seed:        `{"mcp":{"api":{"type":"local","command":["old"],"enabled":true,"timeout":30}}}`,
			desired:     []MCPServer{{Name: "api", Command: "new"}},
			owned:       []string{"api"},
			mustSurvive: `"timeout"`,
			mustUpdate:  `"new"`,
		},
		{
			name:        "crush",
			a:           crushAdapter,
			file:        "crush.json",
			seed:        `{"mcp":{"api":{"type":"stdio","command":"old","timeout":30}}}`,
			desired:     []MCPServer{{Name: "api", Command: "new"}},
			owned:       []string{"api"},
			mustSurvive: `"timeout"`,
			mustUpdate:  `"new"`,
		},
		{
			name:        "forge",
			a:           forgeAdapter,
			file:        "forge.json",
			seed:        `{"mcpServers":{"api":{"command":"old","disable":false,"timeout":30}}}`,
			desired:     []MCPServer{{Name: "api", Command: "new"}},
			owned:       []string{"api"},
			mustSurvive: `"timeout"`,
			mustUpdate:  `"new"`,
		},
		{
			name:        "kilo",
			a:           kiloAdapter,
			file:        "kilo.jsonc",
			seed:        `{"mcp":{"api":{"type":"local","command":["old"],"enabled":true,"timeout":30}}}`,
			desired:     []MCPServer{{Name: "api", Command: "new"}},
			owned:       []string{"api"},
			mustSurvive: `"timeout"`,
			mustUpdate:  `"new"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, tc.file, tc.seed)
			if _, err := tc.a.Apply(p, tc.desired, tc.owned, false); err != nil {
				t.Fatal(err)
			}
			body := read(t, p)
			if !strings.Contains(body, tc.mustUpdate) {
				t.Errorf("%s: modeled field should have updated to %q:\n%s", tc.name, tc.mustUpdate, body)
			}
			if !strings.Contains(body, tc.mustSurvive) {
				t.Errorf("%s: unmodeled key should survive update:\n%s", tc.name, body)
			}
		})
	}
}

// TestCodexUpdatePreservesExtraKeys pins the codex TOML overlay on the update
// path: updating a codex entry must preserve unmodeled keys like
// startup_timeout_sec.
func TestCodexUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "codex_update.toml",
		`[mcp_servers.api]
command = 'old'
args = ['serve']
startup_timeout_sec = 120
`)
	desired := []MCPServer{{Name: "api", Command: "new", Args: []string{"serve"}}}
	if _, err := (codexAdapter{}).Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "new") {
		t.Error("command should have updated to 'new'")
	}
	if !strings.Contains(body, "startup_timeout_sec") {
		t.Errorf("codex update dropped unmodeled startup_timeout_sec:\n%s", body)
	}
}

// TestHermesUpdatePreservesExtraKeys pins the hermes YAML overlay on the
// update path.
func TestHermesUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "hermes_update.yaml",
		"mcp_servers:\n  api:\n    command: old\n    enabled: true\n    timeout: 30\n")
	desired := []MCPServer{{Name: "api", Command: "new"}}
	if _, err := (hermesAdapter{}).Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "new") {
		t.Error("command should have updated to 'new'")
	}
	if !strings.Contains(body, "timeout") {
		t.Errorf("hermes update dropped unmodeled timeout:\n%s", body)
	}
}

// TestForgeUpdatePreservesDisableTrue pins a real bug: forge's `disable` flag
// was in managedKeys and forgeEntryFrom hardcoded Disable:false, so any update
// silently reset the user's disable:true to false. The fix removes `disable`
// from managedKeys and uses omitempty so entryFrom no longer emits it.
func TestForgeUpdatePreservesDisableTrue(t *testing.T) {
	p := writeFile(t, "forge_disable.json", `{"mcpServers":{"api":{"command":"old","disable":true}}}`)
	desired := []MCPServer{{Name: "api", Command: "new"}}
	if _, err := forgeAdapter.Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "new") {
		t.Error("command should have updated to 'new'")
	}
	if !strings.Contains(body, `"disable": true`) {
		t.Errorf("user's disable:true should survive an update:\n%s", body)
	}
}

// TestEnabledFlagSurvivesUpdate pins the systemic fix: the enabled/disabled
// flag is user-owned, not managed by mcphub. Updating an entry must not reset
// the user's enabled:false to true.
func TestEnabledFlagSurvivesUpdate(t *testing.T) {
	cases := []struct {
		name    string
		a       Adapter
		file    string
		seed    string
		desired []MCPServer
		owned   []string
		// A string that must NOT appear (proving enabled was NOT overwritten to true).
		mustNotContain string
		// A string confirming the modeled field was updated.
		mustUpdate string
	}{
		{
			name:           "opencode",
			a:              opencodeAdapter,
			file:           "oc_disable.json",
			seed:           `{"mcp":{"api":{"type":"local","command":["old"],"enabled":false}}}`,
			desired:        []MCPServer{{Name: "api", Command: "new"}},
			owned:          []string{"api"},
			mustNotContain: `"enabled": true`,
			mustUpdate:     `"new"`,
		},
		{
			name:           "kilo",
			a:              kiloAdapter,
			file:           "kilo_disable.jsonc",
			seed:           `{"mcp":{"api":{"type":"local","command":["old"],"enabled":false}}}`,
			desired:        []MCPServer{{Name: "api", Command: "new"}},
			owned:          []string{"api"},
			mustNotContain: `"enabled": true`,
			mustUpdate:     `"new"`,
		},
		{
			name:           "hermes",
			a:              hermesAdapter{},
			file:           "hermes_disable.yaml",
			seed:           "mcp_servers:\n  api:\n    command: old\n    enabled: false\n",
			desired:        []MCPServer{{Name: "api", Command: "new"}},
			owned:          []string{"api"},
			mustNotContain: "enabled: true",
			mustUpdate:     "new",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, tc.file, tc.seed)
			if _, err := tc.a.Apply(p, tc.desired, tc.owned, false); err != nil {
				t.Fatal(err)
			}
			body := read(t, p)
			if !strings.Contains(body, tc.mustUpdate) {
				t.Errorf("%s: modeled field should have updated:\n%s", tc.name, body)
			}
			if strings.Contains(body, tc.mustNotContain) {
				t.Errorf("%s: enabled flag was reset to true — should be user-owned:\n%s", tc.name, body)
			}
		})
	}
}

// TestQwenUpdatePreservesExtraKeys pins the qwen overlay on the update path.
func TestQwenUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "qwen_update.json",
		`{"mcpServers":{"api":{"command":"old","timeout":30,"headers":{"Authorization":"Bearer X"}}}}`)
	desired := []MCPServer{{Name: "api", Command: "new"}}
	if _, err := qwenAdapter.Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "new") {
		t.Error("command should have updated to 'new'")
	}
	if !strings.Contains(body, "Bearer X") || !strings.Contains(body, `"timeout"`) {
		t.Errorf("qwen update dropped unmodeled keys:\n%s", body)
	}
}

// TestGeminiUpdatePreservesExtraKeys pins the gemini overlay on the update path.
func TestGeminiUpdatePreservesExtraKeys(t *testing.T) {
	p := writeFile(t, "gemini_update.json",
		`{"mcpServers":{"api":{"command":"old","timeout":30,"headers":{"Authorization":"Bearer X"}}}}`)
	desired := []MCPServer{{Name: "api", Command: "new"}}
	if _, err := geminiAdapter.Apply(p, desired, []string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, "new") {
		t.Error("command should have updated to 'new'")
	}
	if !strings.Contains(body, "Bearer X") || !strings.Contains(body, `"timeout"`) {
		t.Errorf("gemini update dropped unmodeled keys:\n%s", body)
	}
}

// TestKindsReturnsExactSet pins the exact membership and order of Kinds() so
// a dropped or reordered kind is caught immediately.
func TestKindsReturnsExactSet(t *testing.T) {
	got := Kinds()
	want := []string{"claude", "opencode", "codex", "crush", "forge", "hermes", "copilot", "qwen", "gemini", "kilo", "kimi", "local-agent"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Kinds() = %v, want %v", got, want)
	}
}

// Regression: kimi dropped `environment` for remote entries on write while
// reading it back for every entry, losing env and churning on every sync.
func TestKimiRemoteEnvConverges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	desired := []MCPServer{
		{Name: "r", URL: "http://127.0.0.1:9000/mcp", Env: map[string]string{"TOKEN": "abc"}},
	}

	adapter := kimiAdapter{}
	plan, err := adapter.Apply(path, desired, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applied {
		t.Fatalf("plan = %+v, want applied", plan)
	}

	servers, err := adapter.List(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Env["TOKEN"] != "abc" {
		t.Fatalf("List after write = %+v, want env TOKEN=abc", servers)
	}

	again, err := adapter.Apply(path, desired, []string{"r"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.HasChanges() {
		t.Fatalf("second dry-run not converged: %+v", again.Changes)
	}
}

func TestDiffUpdateDetailIsRedactedAndBounded(t *testing.T) {
	// An update explains what changes field by field so a dry run is
	// reviewable — but env VALUES never appear (harness env blocks commonly
	// hold credentials): only +added/-removed/~changed key names.
	existing := map[string]MCPServer{"mcphub": {
		Name:    "mcphub",
		Command: "mcphub",
		Env:     map[string]string{"API_TOKEN": "hunter2", "KEEP": "same", "OLD": "x"},
	}}
	desired := []MCPServer{{
		Name:    "mcphub",
		Command: "/opt/homebrew/bin/mcphub",
		Args:    []string{"mcp", "serve"},
		Env:     map[string]string{"API_TOKEN": "hunter3", "KEEP": "same", "NEW": "y"},
	}}
	changes := diff(existing, desired, nil)
	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("expected one update, got %+v", changes)
	}
	detail := changes[0].Detail
	for _, want := range []string{
		`command "mcphub" → "/opt/homebrew/bin/mcphub"`,
		"args [] → [mcp serve]",
		"~API_TOKEN", "+NEW", "-OLD",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q: %s", want, detail)
		}
	}
	for _, secret := range []string{"hunter2", "hunter3", "same", "KEEP"} {
		if strings.Contains(detail, secret) {
			t.Errorf("detail leaks %q: %s", secret, detail)
		}
	}
	if len(detail) > maxDetailBytes+len("…") {
		t.Errorf("detail exceeds bound: %d bytes", len(detail))
	}

	// Adds and unchanged rows carry no detail.
	addChanges := diff(map[string]MCPServer{}, desired, nil)
	if len(addChanges) != 1 || addChanges[0].Detail != "" {
		t.Errorf("add should have no detail: %+v", addChanges)
	}
}

func TestUpdateDetailRedactsSecretsInArgsAndURLs(t *testing.T) {
	// Args can carry secrets (--token=xyz, --api-key xyz) and URLs can carry
	// them in query strings and userinfo — none may reach Detail (panel
	// review 2026-07-16: both leaked verbatim via %v / raw quoting).
	existing := map[string]MCPServer{"gw": {
		Name: "gw", Command: "gw",
		Args: []string{"serve", "--token=hunter2", "--api-key", "hunter3", "--agent", "claude"},
		URL:  "https://user:pw@api.example.com/mcp?api_key=hunter4#hunter5",
	}}
	desired := []MCPServer{{
		Name: "gw", Command: "gw",
		Args: []string{"serve", "--token=hunter6", "--api-key", "hunter7", "--agent", "claude"},
		URL:  "https://api.example.com/mcp?api_key=hunter8",
	}}
	detail := diff(existing, desired, nil)[0].Detail
	for _, secret := range []string{"hunter2", "hunter3", "hunter4", "hunter5", "hunter6", "hunter7", "hunter8", "user:pw"} {
		if strings.Contains(detail, secret) {
			t.Errorf("detail leaks %q: %s", secret, detail)
		}
	}
	for _, want := range []string{"--token=***", "--agent", "claude", "api.example.com/mcp"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing structural part %q: %s", want, detail)
		}
	}
}

func TestUpdateDetailSanitizesControlCharsAndRuneBoundary(t *testing.T) {
	// A hostile arg value must not inject terminal escapes or report lines,
	// and byte truncation must never split a multibyte rune.
	existing := map[string]MCPServer{"s": {Name: "s", Command: "old"}}
	desired := []MCPServer{{Name: "s", Command: "evil\x1b[2Jcmd\r\napplied", Args: []string{strings.Repeat("é", 200)}}}
	detail := diff(existing, desired, nil)[0].Detail
	if strings.ContainsAny(detail, "\x1b\r\n") {
		t.Errorf("detail contains control characters: %q", detail)
	}
	if !utf8.ValidString(detail) {
		t.Errorf("detail is not valid UTF-8 after truncation: %q", detail)
	}
	if len(detail) > maxDetailBytes+len("…") {
		t.Errorf("detail exceeds bound: %d", len(detail))
	}
}
