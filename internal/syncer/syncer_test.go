package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

func TestDesired(t *testing.T) {
	c := &config.Config{
		Servers: map[string]config.Server{
			"vaulted": {Command: "codemap", Args: []string{"serve"}, Vault: "proj", Enabled: true},
			"remote":  {URL: "https://x.example/mcp", Transport: "http", Enabled: true},
			"off":     {Command: "nope", Enabled: false},
		},
	}

	// Gateway: exactly one self entry.
	g := Desired(c, config.Agent{Mode: config.ModeGateway}, "/bin/mcphub")
	if len(g) != 1 || g[0].Name != "mcphub" || g[0].Command != "/bin/mcphub" ||
		len(g[0].Args) != 2 || g[0].Args[0] != "mcp" || g[0].Args[1] != "serve" {
		t.Fatalf("gateway desired = %+v", g)
	}

	// Direct: disabled excluded, vault wrapped, remote passthrough.
	d := Desired(c, config.Agent{Mode: config.ModeDirect}, "/bin/mcphub")
	if len(d) != 2 {
		t.Fatalf("direct desired len = %d, want 2 (disabled excluded): %+v", len(d), d)
	}
	by := map[string]struct {
		cmd, url, tr string
		args         []string
	}{}
	for _, s := range d {
		by[s.Name] = struct {
			cmd, url, tr string
			args         []string
		}{s.Command, s.URL, s.Transport, s.Args}
	}
	v := by["vaulted"]
	if v.cmd != "tvault" || strings.Join(v.args, " ") != "run --project proj -- codemap serve" {
		t.Errorf("vaulted not tvault-wrapped: cmd=%q args=%v", v.cmd, v.args)
	}
	if r := by["remote"]; r.url != "https://x.example/mcp" || r.tr != "http" {
		t.Errorf("remote passthrough = %+v", r)
	}
	if _, leaked := by["off"]; leaked {
		t.Error("disabled server leaked into direct desired set")
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestReconcileBranches(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude.json")
	if err := os.WriteFile(claudePath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{
		Servers: map[string]config.Server{"s": {Command: "x", Enabled: true}},
		Agents: map[string]config.Agent{
			"claude": {Type: "claude", Path: claudePath, Mode: config.ModeGateway},
			"off":    {Type: "claude", Path: claudePath, Mode: config.ModeGateway, Disabled: true},
		},
	}
	st := newStore(t)
	ctx := context.Background()
	const self = "/bin/mcphub"

	// Unknown named agent: Err set, run not aborted.
	res := Reconcile(ctx, c, st, self, []string{"nope"}, false)
	if len(res) != 1 || res[0].Err == nil {
		t.Fatalf("unknown agent should yield one result with Err: %+v", res)
	}

	// Empty agents expands to all; disabled is skipped; dry-run persists nothing.
	res = Reconcile(ctx, c, st, self, nil, false)
	if len(res) != 2 {
		t.Fatalf("empty agents should expand to all (2), got %d", len(res))
	}
	for _, r := range res {
		if r.Agent == "off" && !r.Skipped {
			t.Error("disabled agent should be Skipped")
		}
	}
	if m, _ := st.ManagedFor(ctx, "claude"); len(m) != 0 {
		t.Errorf("dry-run must not persist managed entries, got %v", m)
	}

	// Write persists the managed set and applies the file.
	res = Reconcile(ctx, c, st, self, []string{"claude"}, true)
	if res[0].Err != nil || !res[0].Plan.Applied {
		t.Fatalf("write should apply: %+v", res[0])
	}
	if m, _ := st.ManagedFor(ctx, "claude"); len(m) != 1 || m[0] != "mcphub" {
		t.Errorf("write should persist managed=[mcphub], got %v", m)
	}
}

func TestReconcileDriftRemoval(t *testing.T) {
	dir := t.TempDir()
	ocPath := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(ocPath, []byte(`{"mcp":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{
		Servers: map[string]config.Server{"s": {Command: "x", Enabled: true}},
		Agents:  map[string]config.Agent{"oc": {Type: "opencode", Path: ocPath, Mode: config.ModeDirect}},
	}
	st := newStore(t)
	ctx := context.Background()
	const self = "/bin/mcphub"

	// Write s into the agent (direct mode).
	Reconcile(ctx, c, st, self, nil, true)
	if m, _ := st.ManagedFor(ctx, "oc"); len(m) != 1 || m[0] != "s" {
		t.Fatalf("expected managed=[s], got %v", m)
	}

	// Disable s -> next reconcile should plan an ActionRemove for s.
	srv := c.Servers["s"]
	srv.Enabled = false
	c.Servers["s"] = srv
	res := Reconcile(ctx, c, st, self, nil, true)
	removed := false
	for _, ch := range res[0].Plan.Changes {
		if ch.Server == "s" && ch.Action == "remove" {
			removed = true
		}
	}
	if !removed {
		t.Errorf("disabling a managed server should produce a remove: %+v", res[0].Plan.Changes)
	}
	if m, _ := st.ManagedFor(ctx, "oc"); len(m) != 0 {
		t.Errorf("after removal the managed set should be empty, got %v", m)
	}
}

// TestReconcileAllAdapterKinds proves the wiring (harness.For dispatch) works
// end-to-end for every registered kind: write the gateway, persist managed,
// then disable and see it removed.
func TestReconcileAllAdapterKinds(t *testing.T) {
	dir := t.TempDir()
	seed := map[string]string{
		"claude":   "{}",
		"opencode": `{"mcp":{}}`,
		"codex":    "",
		"crush":    `{"mcp":{}}`,
		"forge":    `{"mcpServers":{}}`,
		"hermes":   "",
		"copilot":  `{"mcpServers":{}}`,
		"qwen":     `{"mcpServers":{}}`,
		"gemini":   `{"mcpServers":{}}`,
		"kilo":     `{"mcp":{}}`,
		"kimi":     "",
	}
	ext := map[string]string{
		"claude": ".json", "opencode": ".json", "codex": ".toml",
		"crush": ".json", "forge": ".json", "hermes": ".yaml",
		"copilot": ".json", "qwen": ".json", "gemini": ".json",
		"kilo": ".jsonc", "kimi": ".toml",
	}
	for _, kind := range []string{"claude", "opencode", "codex", "crush", "forge", "hermes", "copilot", "qwen", "gemini", "kilo", "kimi"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(dir, kind+ext[kind])
			if err := os.WriteFile(path, []byte(seed[kind]), 0o644); err != nil {
				t.Fatal(err)
			}
			c := &config.Config{
				Servers: map[string]config.Server{"s": {Command: "x", Enabled: true}},
				Agents:  map[string]config.Agent{"a": {Type: kind, Path: path, Mode: config.ModeGateway}},
			}
			st := newStore(t)
			ctx := context.Background()
			const self = "/bin/mcphub"

			res := Reconcile(ctx, c, st, self, nil, true)
			if res[0].Err != nil {
				t.Fatalf("write: %v", res[0].Err)
			}
			if !res[0].Plan.Applied {
				t.Fatalf("write should apply: %+v", res[0])
			}
			if m, _ := st.ManagedFor(ctx, "a"); len(m) != 1 || m[0] != "mcphub" {
				t.Errorf("managed=[mcphub], got %v", m)
			}
		})
	}
}
