package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

// serversPtr returns a pointer to a non-nil []string — the shape config.Agent
// expects for a scoped agent (nil = all servers; non-nil empty = none).
func serversPtr(s ...string) *[]string { return &s }

// emptyClaudeConfig writes a minimal claude harness config (no servers) and
// returns its path, so syncer.Reconcile produces a clean plan instead of an
// adapter read error.
func emptyClaudeConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(p, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestScopedServerReport covers registration, enabled/PATH state, remote vs
// stdio, proxied-call counts, unused flagging, and agent routing membership.
func TestScopedServerReport(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	claudePath := emptyClaudeConfig(t)
	servers := map[string]config.Server{
		"live":   {Command: "definitely-not-a-real-binary-xyz", Args: []string{"serve"}, Enabled: true},
		"remote": {URL: "https://mcp.example.com/mcp", Transport: "http", Enabled: true},
		"off":    {Command: "echo", Enabled: false},
		"used":   {Command: "echo", Enabled: true},
	}
	agents := map[string]config.Agent{
		"claude":  {Type: "claude", Path: claudePath}, // unscoped → routes to all enabled
		"scoped":  {Type: "claude", Path: claudePath, Servers: serversPtr("used")},
		"scoped2": {Type: "claude", Path: claudePath, Servers: serversPtr("live")},
	}
	c := &config.Config{Version: 1, Servers: servers, Agents: agents, Expose: config.ExposeAll}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	// Record 3 proxied calls to "used" so proxied_calls is non-zero.
	for range 3 {
		if err := st.RecordCall(ctx, store.CallRecord{Server: "used", Tool: "search", Namespaced: "used__search"}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("unregistered", func(t *testing.T) {
		rep := buildScopedServerReport(ctx, c, st, "ghost", false)
		if rep.Registered {
			t.Errorf("ghost: registered=true, want false")
		}
		if len(rep.Agents) != 0 {
			t.Errorf("ghost: agents=%d, want 0", len(rep.Agents))
		}
		if rep.ProxiedCalls != 0 {
			t.Errorf("ghost: proxied_calls=%d, want 0", rep.ProxiedCalls)
		}
	})

	t.Run("live stdio missing binary", func(t *testing.T) {
		rep := buildScopedServerReport(ctx, c, st, "live", false)
		if !rep.Registered || !rep.Enabled {
			t.Errorf("live: registered=%v enabled=%v, want true/true", rep.Registered, rep.Enabled)
		}
		if rep.OnPath {
			t.Errorf("live: on_path=true, want false (bogus command)")
		}
		if rep.Remote {
			t.Errorf("live: remote=true, want false")
		}
		// enabled + zero calls → unused
		if !rep.Unused {
			t.Errorf("live: unused=false, want true (enabled, never proxied)")
		}
		// unscoped agent routes to it; scoped2 routes to it; scoped does not.
		got := agentNames(rep.Agents)
		want := map[string]bool{"claude": true, "scoped2": true}
		for n := range want {
			if !got[n] {
				t.Errorf("live: agent %q missing from %v", n, got)
			}
		}
		if got["scoped"] {
			t.Errorf("live: scoped agent should not route here, got %v", got)
		}
	})

	t.Run("remote", func(t *testing.T) {
		rep := buildScopedServerReport(ctx, c, st, "remote", false)
		if !rep.OnPath {
			t.Errorf("remote: on_path=false, want true (remote needs no binary)")
		}
		if !rep.Remote {
			t.Errorf("remote: remote=false, want true")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		rep := buildScopedServerReport(ctx, c, st, "off", false)
		if rep.Enabled {
			t.Errorf("off: enabled=true, want false")
		}
		if rep.Unused {
			t.Errorf("off: unused=true, want false (not enabled)")
		}
		if len(rep.Agents) != 0 {
			t.Errorf("off: agents=%d, want 0 (server disabled → no agent routes)", len(rep.Agents))
		}
	})

	t.Run("used with proxied calls", func(t *testing.T) {
		rep := buildScopedServerReport(ctx, c, st, "used", false)
		if rep.ProxiedCalls != 3 {
			t.Errorf("used: proxied_calls=%d, want 3", rep.ProxiedCalls)
		}
		if rep.Unused {
			t.Errorf("used: unused=true, want false (has proxied calls)")
		}
		// unscoped agent + scoped agent both route; scoped2 (live) does not.
		got := agentNames(rep.Agents)
		if !got["claude"] || !got["scoped"] {
			t.Errorf("used: missing routing agents, got %v", got)
		}
		if got["scoped2"] {
			t.Errorf("used: scoped2 should not route here, got %v", got)
		}
	})
}

// TestScopedServerReportNoStore asserts a nil store is tolerated (proxied
// calls stay zero, no panic) — the path used when the db can't be opened.
func TestScopedServerReportNoStore(t *testing.T) {
	ctx := context.Background()
	c := &config.Config{
		Version: 1,
		Servers: map[string]config.Server{"live": {Command: "echo", Enabled: true}},
		Expose:  config.ExposeAll,
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	rep := buildScopedServerReport(ctx, c, nil, "live", false)
	if rep.ProxiedCalls != 0 {
		t.Errorf("nil store: proxied_calls=%d, want 0", rep.ProxiedCalls)
	}
	if !rep.Registered || !rep.Enabled {
		t.Errorf("nil store: registered=%v enabled=%v", rep.Registered, rep.Enabled)
	}
}

// TestAgentRoutesTo covers the unscoped/scoped membership rule directly.
func TestAgentRoutesTo(t *testing.T) {
	cases := []struct {
		name   string
		agent  config.Agent
		server string
		want   bool
	}{
		{"unscoped", config.Agent{}, "anything", true},
		{"scoped-in", config.Agent{Servers: serversPtr("a", "b")}, "a", true},
		{"scoped-out", config.Agent{Servers: serversPtr("a", "b")}, "c", false},
		{"scoped-empty", config.Agent{Servers: serversPtr()}, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentRoutesTo(tc.agent, tc.server); got != tc.want {
				t.Errorf("agentRoutesTo(%+v, %q) = %v, want %v", tc.agent, tc.server, got, tc.want)
			}
		})
	}
}

func agentNames(in []serverAgentState) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, a := range in {
		out[a.Agent] = true
	}
	return out
}

func TestDoctorScopedProbeExitStatusAndJSON(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          string
		wantErr       bool
		wantHandshake bool
		wantTools     int
	}{
		{name: "successful handshake", mode: "serve", wantHandshake: true, wantTools: 1},
		{name: "failed handshake", mode: "fail", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "mcphub.yaml")
			c := &config.Config{
				Version:        1,
				ConnectTimeout: "3s",
				Servers: map[string]config.Server{
					"subject": {
						Command: os.Args[0],
						Args:    []string{"-test.run=^TestDoctorMCPHelper$"},
						Env: map[string]string{
							"MCPHUB_DOCTOR_HELPER": tc.mode,
							"TAVILY_API_KEY":       "doctor-test-secret",
						},
						Enabled: true,
					},
				},
				Agents: map[string]config.Agent{},
			}
			if err := config.Save(cfgPath, c); err != nil {
				t.Fatal(err)
			}

			stdout, stderr, err := runRootSeparate(
				"--config", cfgPath,
				"--db", filepath.Join(dir, "mcphub.db"),
				"--json",
				"doctor", "--server", "subject", "--probe",
			)
			if (err != nil) != tc.wantErr {
				t.Fatalf("doctor error = %v, wantErr %t; stdout=%s stderr=%s", err, tc.wantErr, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("Root polluted stderr before Execute's single error emitter: %q", stderr)
			}
			var rep scopedServerReport
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("doctor stdout is not useful JSON: %v\n%s", err, stdout)
			}
			if rep.HandshakeOK == nil || *rep.HandshakeOK != tc.wantHandshake {
				t.Errorf("handshake_ok = %v, want %t", rep.HandshakeOK, tc.wantHandshake)
			}
			if tc.wantHandshake {
				if rep.ToolCount == nil || *rep.ToolCount != tc.wantTools {
					t.Errorf("tool_count = %v, want %d", rep.ToolCount, tc.wantTools)
				}
			} else {
				if !strings.Contains(rep.ProbeError, "downstream stderr:") ||
					!strings.Contains(rep.ProbeError, "vault is locked") {
					t.Errorf("probe_error lacks actionable child diagnostic: %q", rep.ProbeError)
				}
				if strings.Contains(rep.ProbeError, "doctor-test-secret") {
					t.Errorf("probe_error leaked configured secret: %q", rep.ProbeError)
				}
			}
			if strings.Contains(stdout+stderr, "doctor-test-secret") {
				t.Errorf("doctor output leaked configured secret: stdout=%s stderr=%s", stdout, stderr)
			}
		})
	}
}

func TestDoctorJSONReturnsErrorWhenProbeCheckFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcphub.yaml")
	c := &config.Config{
		Version:        1,
		ConnectTimeout: "3s",
		Servers: map[string]config.Server{
			"subject": {
				Command: os.Args[0],
				Args:    []string{"-test.run=^TestDoctorMCPHelper$"},
				Env:     map[string]string{"MCPHUB_DOCTOR_HELPER": "fail"},
				Enabled: true,
			},
		},
		Agents: map[string]config.Agent{},
	}
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runRootSeparate(
		"--config", cfgPath,
		"--db", filepath.Join(dir, "mcphub.db"),
		"--json",
		"doctor", "--probe",
	)
	if err == nil {
		t.Fatalf("doctor --json --probe returned nil error; stdout=%s", stdout)
	}
	var checks []checkResult
	if jsonErr := json.Unmarshal([]byte(stdout), &checks); jsonErr != nil {
		t.Fatalf("doctor stdout is not valid JSON: %v\n%s", jsonErr, stdout)
	}
	foundFailedProbe := false
	for _, check := range checks {
		if check.Name == "probe:subject" && !check.OK {
			foundFailedProbe = true
		}
	}
	if !foundFailedProbe {
		t.Errorf("doctor JSON lacks failed probe check: %+v", checks)
	}
}

// TestDoctorMCPHelper is re-executed in a subprocess by the doctor tests.
// Keeping both success and failure modes inside the test binary avoids relying
// on any user-installed MCP server.
func TestDoctorMCPHelper(t *testing.T) {
	switch os.Getenv("MCPHUB_DOCTOR_HELPER") {
	case "":
		return
	case "fail":
		fmt.Fprintf(os.Stderr, "TAVILY_API_KEY=%s\nvault is locked; start the local agent\n", os.Getenv("TAVILY_API_KEY"))
		os.Exit(3)
	case "serve":
		server := mcp.NewServer(&mcp.Implementation{Name: "doctor-helper", Version: "1"}, nil)
		server.AddTool(
			&mcp.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
			},
		)
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode")
		os.Exit(2)
	}
}

func runRootSeparate(args ...string) (stdout, stderr string, err error) {
	resetFlags()
	root := Root()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}
