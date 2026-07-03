package cli

import (
	"fmt"
	"os"
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
// their conventional config locations, derived from harness.DefaultPath.

func defaultAgentSpecs() []agentSpec {
	var specs []agentSpec
	for _, kind := range harness.Kinds() {
		specs = append(specs, agentSpec{Name: kind, Type: kind, Path: harness.DefaultPath(kind)})
	}
	return specs
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
