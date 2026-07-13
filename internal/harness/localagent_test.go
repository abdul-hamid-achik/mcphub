package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalAgentAdapterRoundTripAndPreservesUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := `ollama:
  model: qwen3.5:2b
custom_top: keep
servers:
  - name: keep
    command: old
    custom_timeout: 42
  - name: manual
    command: manual-server
`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	desired := []MCPServer{
		{Name: "keep", Command: "new", Args: []string{"serve"}, Env: map[string]string{"B": "2", "A": "1"}},
		{Name: "remote", URL: "http://127.0.0.1:9000/mcp", Transport: "http"},
	}

	adapter := localAgentAdapter{}
	plan, err := adapter.Apply(path, desired, []string{"keep"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applied || plan.Backup == "" {
		t.Fatalf("plan = %+v, want applied with backup", plan)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"custom_top: keep", "custom_timeout: 42", "manual-server", "command: new", "streamable-http"} {
		if !strings.Contains(text, want) {
			t.Errorf("updated config missing %q:\n%s", want, text)
		}
	}

	servers, err := adapter.List(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 {
		t.Fatalf("List returned %d servers, want 3: %+v", len(servers), servers)
	}
	var keep, remote MCPServer
	for _, server := range servers {
		switch server.Name {
		case "keep":
			keep = server
		case "remote":
			remote = server
		}
	}
	if keep.Command != "new" || len(keep.Args) != 1 || keep.Env["A"] != "1" {
		t.Fatalf("keep server = %+v", keep)
	}
	if remote.URL == "" || remote.Transport != "http" {
		t.Fatalf("remote server = %+v", remote)
	}
}

func TestLocalAgentAdapterDryRunDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := []byte("servers: []\n")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := (localAgentAdapter{}).Apply(path, []MCPServer{{Name: "hub", Command: "mcphub"}}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges() || plan.Applied {
		t.Fatalf("dry-run plan = %+v", plan)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(seed) {
		t.Fatalf("dry run changed file: %q", got)
	}
}

// Regression: a remote server with env and no explicit transport must
// converge — the write path used to drop env for remotes and never default
// the transport, so every sync reported "update" forever.
func TestLocalAgentRemoteEnvAndTransportConverge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("servers: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	desired := []MCPServer{
		{Name: "r", URL: "http://127.0.0.1:9000/mcp", Env: map[string]string{"TOKEN": "abc"}},
	}

	adapter := localAgentAdapter{}
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
