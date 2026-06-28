package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	applyLifecycle(t, claudeAdapter{}, p, "numFlag")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written server should survive")
	}
}

func TestOpencodeLifecycle(t *testing.T) {
	p := writeFile(t, "opencode.json", `{"$schema":"x","mcp":{},"model":"a/b"}`)
	applyLifecycle(t, opencodeAdapter{}, p, "model")
}

func TestCodexLifecycle(t *testing.T) {
	p := writeFile(t, "config.toml", "model = \"gpt\"\n[mcp_servers.existing]\ncommand = \"keepme\"\n")
	applyLifecycle(t, codexAdapter{}, p, "keepme")
}

func TestCrushLifecycle(t *testing.T) {
	p := writeFile(t, "crush.json", `{"$schema":"x","mcp":{"keep":{"command":"k","type":"stdio"}},"options":{"a":1}}`)
	applyLifecycle(t, crushAdapter{}, p, "options")
	if !strings.Contains(read(t, p), "keep") {
		t.Error("hand-written crush server should survive")
	}
}

func TestListReadsLocalAndRemote(t *testing.T) {
	// type:"stdio" must NOT become a transport; only a url-based remote does.
	p := writeFile(t, "claude.json", `{"mcpServers":{
		"local":{"command":"c","args":["x"],"type":"stdio"},
		"remote":{"type":"http","url":"https://e.com"}}}`)
	got, err := claudeAdapter{}.List(p)
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
	got, err := claudeAdapter{}.List(filepath.Join(t.TempDir(), "nope.json"))
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
		{"a", ActionUnchanged},
		{"b", ActionUpdate},
		{"c", ActionAdd},
		{"gone_present", ActionRemove},
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
		{"claude", claudeAdapter{}, "claude.json", `{"mcpServers":{}}`},
		{"crush", crushAdapter{}, "crush.json", `{"mcp":{}}`},
		{"opencode", opencodeAdapter{}, "opencode.json", `{"mcp":{}}`},
		{"codex", codexAdapter{}, "config.toml", ""},
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
	a := claudeAdapter{}
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
	a := claudeAdapter{}
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
	a := claudeAdapter{}
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
	applyLifecycle(t, forgeAdapter{}, p, "keep")

	// a freshly written entry carries forge's `disable: false` flag
	p2 := writeFile(t, "forge2.mcp.json", `{"mcpServers":{}}`)
	a := forgeAdapter{}
	if _, err := a.Apply(p2, gateway, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, p2), `"disable": false`) {
		t.Errorf("forge entry should carry disable:false:\n%s", read(t, p2))
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
	a := opencodeAdapter{}
	if _, err := a.Apply(p, desired, nil, false); err != nil {
		t.Fatal(err)
	}
	body := read(t, p)
	if !strings.Contains(body, `"codemap"`) || !strings.Contains(body, `"serve"`) {
		t.Errorf("command+args should flatten into one array: %s", body)
	}
}
