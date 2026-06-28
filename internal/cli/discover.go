package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
)

// agentSpec is a well-known harness and where its config lives by default.
type agentSpec struct {
	Name string
	Type string
	Path string
}

// defaultAgentSpecs lists the harnesses mcphub knows how to read/write and
// their conventional config locations.
// xdgConfigHome returns $XDG_CONFIG_HOME, or ~/.config when it is unset — the
// standard base directory the XDG-based harnesses (opencode, crush) live under.
func xdgConfigHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func defaultAgentSpecs() []agentSpec {
	home, _ := os.UserHomeDir()
	xdg := xdgConfigHome()
	return []agentSpec{
		{"claude", "claude", filepath.Join(home, ".claude.json")},
		{"opencode", "opencode", filepath.Join(xdg, "opencode", "opencode.json")},
		{"codex", "codex", filepath.Join(home, ".codex", "config.toml")},
		{"crush", "crush", filepath.Join(xdg, "crush", "crush.json")},
		{"forge", "forge", filepath.Join(home, "forge", ".mcp.json")},
		{"hermes", "hermes", filepath.Join(home, ".hermes", "config.yaml")},
	}
}

// discoverFromAgents scans every known harness config that exists, unions the
// MCP servers it finds, and builds a Config wiring those agents up in gateway
// mode. It returns the config plus a human-readable note per scanned agent.
func discoverFromAgents() (*config.Config, []string, error) {
	servers := map[string]config.Server{}
	agents := map[string]config.Agent{}
	var notes []string

	for _, spec := range defaultAgentSpecs() {
		if _, err := os.Stat(spec.Path); err != nil {
			continue // harness not installed / no config
		}
		adapter, err := harness.For(spec.Type)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%-9s skipped (%v)", spec.Name, err))
			continue
		}
		found, err := adapter.List(spec.Path)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%-9s read error (%v)", spec.Name, err))
			continue
		}
		agents[spec.Name] = config.Agent{Type: spec.Type, Path: shortenHome(spec.Path), Mode: config.ModeGateway}
		added := 0
		for _, m := range found {
			if m.Name == "mcphub" {
				continue // never manage the gateway as a downstream of itself
			}
			if _, exists := servers[m.Name]; exists {
				continue // first harness to declare a name wins
			}
			servers[m.Name] = config.Server{
				Command:     m.Command,
				Args:        m.Args,
				Env:         m.Env,
				URL:         m.URL,
				Transport:   m.Transport,
				Enabled:     true,
				Description: "discovered from " + spec.Name,
			}
			added++
		}
		notes = append(notes, fmt.Sprintf("%-9s %d servers (%d new)", spec.Name, len(found), added))
	}

	if len(agents) == 0 {
		return nil, notes, fmt.Errorf("no known agent configs found to import from")
	}
	return &config.Config{Version: 1, Servers: servers, Agents: agents}, notes, nil
}

// shortenHome replaces a leading home directory with ~ for a portable config.
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
