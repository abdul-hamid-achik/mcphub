package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/syncer"
)

// agentStatus is the per-agent drift summary surfaced by `mcphub status`.
type agentStatus struct {
	Agent   string `json:"agent"`
	Type    string `json:"type"`
	Mode    string `json:"mode"`
	State   string `json:"state"`   // "in sync" | "N pending" | "disabled" | "error"
	Pending int    `json:"pending"` // number of changes a sync would make
	Error   string `json:"error,omitempty"`
}

type statusReport struct {
	Config  string        `json:"config"`
	Expose  string        `json:"expose"`
	Servers int           `json:"servers"`
	Enabled int           `json:"enabled"`
	Agents  []agentStatus `json:"agents"`
	Calls   int64         `json:"calls"`
	Errors  int64         `json:"errors"`
	Tokens  int64         `json:"est_tokens"`
	Unused  []string      `json:"unused_enabled"`
}

func newStatusCmd() *cobra.Command {
	var markdown bool
	var server string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Config, per-agent sync drift, and usage intelligence at a glance",
		Long: `status answers "is everything consistent?". For each agent it does a
read-only dry run and reports whether the agent's config already matches your
mcphub config ("in sync") or has changes pending. It also summarizes recorded
usage and flags enabled servers that have never been called — candidates to
disable so your agents carry less context.

Use --markdown for a report you can paste into notes or an issue, or --json for
a machine-readable one.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cfgPath, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()

			// Scoped single-server view: which agents route to this server and
			// how many calls the gateway has proxied to it — a cheap "am I wired
			// in?" answer for one server.
			if server != "" {
				rep := buildScopedServerReport(ctx, c, st, server, false)
				if flagJSON {
					return printJSON(cmd, rep)
				}
				return renderScopedServerReport(cmd, rep)
			}

			self, _ := syncer.Self()
			results := syncer.Reconcile(ctx, c, st, self, nil, false)
			totals, _ := st.Totals(ctx)
			serverStats, _ := st.ServerStats(ctx)

			expose := c.Expose
			if expose == "" {
				expose = config.ExposeAll
			}
			rep := statusReport{
				Config:  cfgPath,
				Expose:  expose,
				Servers: len(c.Servers),
				Enabled: len(c.EnabledServers()),
				Calls:   totals.Calls,
				Errors:  totals.Errors,
				Tokens:  totals.EstTokens,
				Unused:  unusedServers(c, serverStats),
			}
			for _, r := range results {
				as := agentStatus{Agent: r.Agent, Type: r.Type, Mode: r.Mode}
				switch {
				case r.Err != nil:
					as.State, as.Error = "error", r.Err.Error()
				case r.Skipped:
					as.State = "disabled"
				case r.Plan.HasChanges():
					as.Pending = countChanges(r.Plan)
					as.State = fmt.Sprintf("%d pending", as.Pending)
				default:
					as.State = "in sync"
				}
				rep.Agents = append(rep.Agents, as)
			}

			if flagJSON {
				return printJSON(cmd, rep)
			}
			if markdown {
				return renderStatusMarkdown(cmd, rep)
			}
			return renderStatus(cmd, rep)
		},
	}
	cmd.Flags().BoolVar(&markdown, "markdown", false, "render the report as Markdown (great for notes/issues)")
	cmd.Flags().StringVar(&server, "server", "", "scope to one server: which agents route to it + proxied-call count")
	return cmd
}

func renderStatusMarkdown(cmd *cobra.Command, rep statusReport) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# mcphub status\n\n")
	fmt.Fprintf(out, "- **Config:** `%s`\n", rep.Config)
	fmt.Fprintf(out, "- **Servers:** %d (%d enabled)\n", rep.Servers, rep.Enabled)
	fmt.Fprintf(out, "- **Exposure:** %s\n\n", rep.Expose)

	fmt.Fprintf(out, "## Agents\n\n| Agent | Type | Mode | Sync |\n| --- | --- | --- | --- |\n")
	for _, a := range rep.Agents {
		state := a.State
		if a.Error != "" {
			state = "error: " + a.Error
		}
		fmt.Fprintf(out, "| %s | %s | %s | %s |\n", a.Agent, a.Type, a.Mode, state)
	}

	fmt.Fprintf(out, "\n## Usage\n\n%d calls · %d errors · ~%d est. tokens\n", rep.Calls, rep.Errors, rep.Tokens)
	if len(rep.Unused) > 0 {
		fmt.Fprintf(out, "\n**Unused** (enabled but never called): %s — consider `mcphub disable <name>`.\n", strings.Join(rep.Unused, ", "))
	}
	return nil
}

func renderStatus(cmd *cobra.Command, rep statusReport) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Config:  %s\n", rep.Config)
	fmt.Fprintf(out, "Servers: %d (%d enabled)   Exposure: %s\n\n", rep.Servers, rep.Enabled, rep.Expose)

	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tTYPE\tMODE\tSYNC")
	pending := false
	for _, a := range rep.Agents {
		state := a.State
		if a.Error != "" {
			state = "error: " + a.Error
		}
		if a.Pending > 0 {
			pending = true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Agent, a.Type, a.Mode, state)
	}
	w.Flush()

	fmt.Fprintf(out, "\nUsage:   %d calls, %d errors, ~%d est. tokens\n", rep.Calls, rep.Errors, rep.Tokens)
	if len(rep.Unused) > 0 {
		fmt.Fprintf(out, "Unused:  %s (enabled but never called)\n", strings.Join(rep.Unused, ", "))
		fmt.Fprintln(out, "         → consider `mcphub disable <name>` to shrink agent context.")
	}
	if pending {
		fmt.Fprintln(out, "\nSome agents are out of sync. Run `mcphub sync` to preview, `mcphub sync --write` to apply.")
	}
	return nil
}

// countChanges counts the non-noop changes in a plan.
func countChanges(p harness.Plan) int {
	n := 0
	for _, ch := range p.Changes {
		if ch.Action != harness.ActionUnchanged {
			n++
		}
	}
	return n
}

// unusedServers returns enabled servers that have no recorded tool calls,
// sorted — candidates to disable to reduce context.
func unusedServers(c *config.Config, stats []store.ServerStat) []string {
	called := map[string]bool{}
	for _, s := range stats {
		if s.Calls > 0 {
			called[s.Server] = true
		}
	}
	var unused []string
	for _, name := range c.EnabledServers() {
		if !called[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	return unused
}
