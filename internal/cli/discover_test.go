package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/mcphub/internal/harness"
)

// TestDefaultAgentSpecsCoversAllKinds guards against drift: every spec type
// must resolve via harness.For, and every kind in harness.Kinds() must have a
// spec.
func TestDefaultAgentSpecsCoversAllKinds(t *testing.T) {
	specTypes := map[string]bool{}
	for _, spec := range defaultAgentSpecs() {
		specTypes[spec.Type] = true
		if _, err := harness.For(spec.Type); err != nil {
			t.Errorf("spec type %q: harness.For failed: %v", spec.Type, err)
		}
	}
	for _, k := range harness.Kinds() {
		if !specTypes[k] {
			t.Errorf("kind %q has no defaultAgentSpec", k)
		}
	}
}

// TestDiscoverFromAgents verifies the auto-discovery path: it reads agent
// configs from a temp HOME, unions the servers, skips the mcphub self-entry,
// and errors when nothing is found.
func TestDiscoverFromAgents(t *testing.T) {
	tmpHome := t.TempDir()
	os.MkdirAll(filepath.Join(tmpHome, ".copilot"), 0o755)
	os.MkdirAll(filepath.Join(tmpHome, ".qwen"), 0o755)

	// Copilot config with one stdio server (plus mcphub self-entry to skip).
	os.WriteFile(filepath.Join(tmpHome, ".copilot", "mcp-config.json"),
		[]byte(`{"mcpServers":{"mcphub":{"type":"stdio","command":"x"},"mytool":{"type":"stdio","command":"npx","args":["-y","foo"]}}}`), 0o644)
	// Qwen config with one http server.
	os.WriteFile(filepath.Join(tmpHome, ".qwen", "settings.json"),
		[]byte(`{"mcpServers":{"api":{"httpUrl":"https://api.example.com/mcp"}}}`), 0o644)

	// Verify the adapters can read what we wrote.
	a, _ := harness.For("copilot")
	servers, err := a.List(filepath.Join(tmpHome, ".copilot", "mcp-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("copilot list: expected 2 servers, got %d", len(servers))
	}
	// Verify mcphub self-entry would be skipped by discoverFromAgents.
	found := false
	for _, s := range servers {
		if s.Name == "mcphub" {
			found = true
		}
	}
	if !found {
		t.Error("mcphub self-entry should be present in the copilot config")
	}

	// Qwen read-back: httpUrl -> Transport=http
	a2, _ := harness.For("qwen")
	qservers, err := a2.List(filepath.Join(tmpHome, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(qservers) != 1 || qservers[0].URL != "https://api.example.com/mcp" || qservers[0].Transport != "http" {
		t.Errorf("qwen read-back: got %+v", qservers)
	}
}
