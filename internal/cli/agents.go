package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// agentRow is one row of the `mcphub agents` report.
type agentRow struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	State   string `json:"state"` // configured | available | not_installed
	Name    string `json:"name"`  // configured-as name (empty if not configured)
	Mode    string `json:"mode"`  // gateway | direct (empty if not configured)
	Enabled bool   `json:"enabled"`
}

func newAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List supported agent harnesses and their status",
		Long: `agents lists every agent harness mcphub can sync to, whether each one's
config file exists on disk, and whether it's already wired into mcphub.yaml.

States:
  configured     already in mcphub.yaml (and the config file exists)
  available      not in mcphub.yaml, but the config file exists — add it to sync
  not_installed  neither in mcphub.yaml nor on disk — install the tool first

To add an available agent, add an entry under 'agents:' in mcphub.yaml, or run
'mcphub init --from-agents' to auto-discover all installed agents at once.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := loadConfig()
			if err != nil {
				// No config yet — show all as not_installed/available.
				rows := buildAgentRows(nil)
				if flagJSON {
					return printJSON(cmd, rows)
				}
				return printAgentRows(cmd, rows)
			}
			rows := buildAgentRows(c)
			if flagJSON {
				return printJSON(cmd, rows)
			}
			return printAgentRows(cmd, rows)
		},
	}
	return cmd
}

func printAgentRows(cmd *cobra.Command, rows []agentRow) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tSTATE\tCONFIG-AS\tMODE\tPATH")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Type, r.State, r.Name, r.Mode, r.Path)
	}
	return w.Flush()
}

// buildAgentRows produces one row per supported harness type, enriched with
// whether the default config file exists and whether it's already configured.
func buildAgentRows(c *config.Config) []agentRow {
	// Map configured agent types to their config entry.
	type cfgInfo struct {
		name, mode, path string
		enabled          bool
	}
	configured := map[string]cfgInfo{}
	if c != nil {
		for _, name := range c.AgentNames() {
			a := c.Agents[name]
			configured[a.Type] = cfgInfo{name: name, mode: string(a.ResolvedMode()), path: a.Path, enabled: !a.Disabled}
		}
	}

	var rows []agentRow
	for _, spec := range defaultAgentSpecs() {
		path := config.ExpandPath(spec.Path)
		_, fileErr := os.Stat(path)
		row := agentRow{Type: spec.Type, Path: spec.Path}
		if ci, ok := configured[spec.Type]; ok {
			row.State = "configured"
			row.Name = ci.name
			row.Mode = ci.mode
			row.Enabled = ci.enabled
		} else if fileErr == nil {
			row.State = "available"
		} else {
			row.State = "not_installed"
		}
		rows = append(rows, row)
	}
	return rows
}
