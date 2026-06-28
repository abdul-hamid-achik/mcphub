package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
)

// --- init -----------------------------------------------------------------

const starterConfig = `# mcphub.yaml — the single source of truth for your MCP servers.
# Edit here (or run 'mcphub studio'), then 'mcphub sync' to push to every agent.
version: 1

# How the gateway advertises tools to agents:
#   all  (default) — mount every downstream tool as 'server__tool'
#   lazy           — advertise only mcphub's meta-tools; agents discover with
#                    mcphub_search_tools and invoke via mcphub_call_tool (saves tokens)
expose: all

servers:
  codemap:
    command: codemap
    args: [serve]
    enabled: true
    description: Code knowledge graph
    tags: [code, search]
  vecgrep:
    command: vecgrep
    args: [serve, --mcp]
    enabled: true
    description: Semantic code search
    tags: [code, search]
  glyph:
    command: glyph
    args: [mcp]
    enabled: false
    description: TUI behavior testing

# Optional named bundles you can enable together with 'mcphub use <group>'.
groups:
  coding: [codemap, vecgrep]

# Agent harnesses mcphub keeps in sync.
#   mode: gateway  -> the agent sees ONLY mcphub, which proxies the rest (saves tokens)
#   mode: direct   -> every enabled server is written straight into the agent
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json
    mode: direct
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
`

func newInitCmd() *cobra.Command {
	var force, fromAgents bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter mcphub.yaml (or import from your agents)",
		Long: `init creates mcphub.yaml.

By default it writes a small starter config. With --from-agents it scans your
installed harness configs (Claude Code, opencode, Codex, Crush), unions every
MCP server they already declare, and wires those agents up in gateway mode —
so you can adopt mcphub without retyping what you already have.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := configPath()
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if fromAgents {
				c, notes, err := discoverFromAgents()
				for _, n := range notes {
					fmt.Fprintf(out, "  %s\n", n)
				}
				if err != nil {
					return err
				}
				if err := config.Save(path, c); err != nil {
					return err
				}
				fmt.Fprintf(out, "Imported %d servers across %d agents into %s\nNext: `mcphub sync` (dry-run) to preview.\n",
					len(c.Servers), len(c.Agents), path)
				return nil
			}
			if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(out, "Wrote %s\nNext: edit it, then `mcphub sync` (dry-run) to preview.\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().BoolVar(&fromAgents, "from-agents", false, "import servers from your installed harness configs")
	return cmd
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

// --- list -----------------------------------------------------------------

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured servers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := loadConfig()
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, c.Servers)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "SERVER\tSTATE\tKIND\tTARGET\tTAGS\tDESCRIPTION")
			for _, name := range c.ServerNames() {
				s := c.Servers[name]
				state := "off"
				if s.Enabled {
					state = "on"
				}
				kind, target := "stdio", s.Command
				if s.IsRemote() {
					kind, target = "remote", s.URL
				}
				if s.UsesVault() {
					target += " [vault:" + s.Vault + "]"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", name, state, kind, target, strings.Join(s.Tags, ","), s.Description)
			}
			return w.Flush()
		},
	}
}

// --- enable / disable -----------------------------------------------------

func newEnableCmd() *cobra.Command  { return toggleCmd("enable", true) }
func newDisableCmd() *cobra.Command { return toggleCmd("disable", false) }

func toggleCmd(verb string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <server>",
		Short: fmt.Sprintf("%s a server in mcphub.yaml", verb),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			name := args[0]
			s, ok := c.Servers[name]
			if !ok {
				return fmt.Errorf("no such server %q (see `mcphub list`)", name)
			}
			s.Enabled = enabled
			c.Servers[name] = s
			if err := config.Save(path, c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %sd. Run `mcphub sync` to apply.\n", name, verb)
			return nil
		},
	}
}

// --- stats ----------------------------------------------------------------

func newStatsCmd() *cobra.Command {
	var byTools bool
	var recent int
	var since string
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show local tool-call intelligence",
		Long: `stats summarizes the tool calls the gateway has recorded.

By default it shows all-time totals and a per-server breakdown. Use --tools for
a per-tool breakdown (which exact tools cost the most), --recent N to list the
most recent N calls, or --since to limit to a recent window (e.g. --since 24h,
--since 7d) — handy for "which servers are earning their keep lately".`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			window, err := parseSince(since)
			if err != nil {
				return err
			}
			totals, err := st.TotalsSince(ctx, window)
			if err != nil {
				return err
			}
			servers, err := st.ServerStatsSince(ctx, window)
			if err != nil {
				return err
			}
			tools, err := st.ToolStatsSince(ctx, window)
			if err != nil {
				return err
			}
			var recents []recentCall
			if recent > 0 {
				rows, err := st.RecentCalls(ctx, recent)
				if err != nil {
					return err
				}
				for _, r := range rows {
					recents = append(recents, recentCall{TS: r.Ts, Namespaced: r.Namespaced, OK: r.Ok, DurationMs: r.DurationMs, EstTokens: r.EstTokens})
				}
			}
			if flagJSON {
				return printJSON(cmd, map[string]any{"totals": totals, "servers": servers, "tools": tools, "recent": recents})
			}
			out := cmd.OutOrStdout()
			scope := "all time"
			if window > 0 {
				scope = "last " + since
			}
			fmt.Fprintf(out, "Totals (%s): %d calls, %d errors, ~%d tokens, %dms total\n\n",
				scope, totals.Calls, totals.Errors, totals.EstTokens, totals.TotalMs)
			if totals.Calls == 0 {
				if window > 0 {
					fmt.Fprintf(out, "No tool calls in the last %s.\n", since)
				} else {
					fmt.Fprintln(out, "No tool calls recorded yet. Point an agent at `mcphub mcp serve` and use it.")
				}
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			if byTools {
				fmt.Fprintln(w, "SERVER\tTOOL\tCALLS\tERRORS\tAVG_MS\tEST_TOKENS")
				for _, t := range tools {
					fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\n", t.Server, t.Tool, t.Calls, t.Errors, t.AvgMs, t.EstTokens)
				}
			} else {
				fmt.Fprintln(w, "SERVER\tCALLS\tERRORS\tAVG_MS\tEST_TOKENS")
				for _, s := range servers {
					fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n", s.Server, s.Calls, s.Errors, s.AvgMs, s.EstTokens)
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if recent > 0 {
				fmt.Fprintf(out, "\nRecent %d calls:\n", len(recents))
				rw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
				fmt.Fprintln(rw, "WHEN\tTOOL\tOK\tMS\tTOKENS")
				for _, r := range recents {
					fmt.Fprintf(rw, "%s\t%s\t%t\t%d\t%d\n", r.TS, r.Namespaced, r.OK, r.DurationMs, r.EstTokens)
				}
				rw.Flush()
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&byTools, "tools", false, "break down by individual tool instead of server")
	cmd.Flags().IntVar(&recent, "recent", 0, "also list the N most recent calls")
	cmd.Flags().StringVar(&since, "since", "", "limit to a recent window, e.g. 24h, 90m, 7d (default: all time)")
	return cmd
}

// parseSince parses a lookback window. It accepts any Go duration (e.g. 24h,
// 90m) plus a day suffix (e.g. 7d). An empty string means all time (0).
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err != nil || days < 0 {
			return 0, fmt.Errorf("invalid --since %q (try 7d, 24h, 90m)", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid --since %q (try 7d, 24h, 90m)", s)
	}
	return d, nil
}

// recentCall is the JSON/printable shape of a recent tool call.
type recentCall struct {
	TS         string `json:"ts"`
	Namespaced string `json:"tool"`
	OK         bool   `json:"ok"`
	DurationMs int64  `json:"duration_ms"`
	EstTokens  int64  `json:"est_tokens"`
}

// --- doctor ---------------------------------------------------------------

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func newDoctorCmd() *cobra.Command {
	var probe bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose config, server availability, and agent targets",
		Long: `doctor checks that your config parses, every enabled server's command is
on PATH, each agent target exists, and the intelligence store opens.

With --probe it goes further: it actually spawns each enabled server, performs
the MCP handshake, and reports how many tools each one exposes (or why it
failed) — a real connectivity check, not just a PATH lookup.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var checks []checkResult

			c, path, err := loadConfig()
			if err != nil {
				checks = append(checks, checkResult{"config", false, err.Error()})
				return reportChecks(cmd, checks)
			}
			checks = append(checks, checkResult{"config", true, path})

			usesVault := false
			for _, name := range c.EnabledServers() {
				s := c.Servers[name]
				if s.IsRemote() {
					checks = append(checks, checkResult{"server:" + name, true, "remote " + s.URL})
					continue
				}
				if s.UsesVault() {
					usesVault = true
				}
				if p, err := exec.LookPath(s.Command); err != nil {
					checks = append(checks, checkResult{"server:" + name, false, "command not on PATH: " + s.Command})
				} else {
					detail := p
					if s.UsesVault() {
						detail += fmt.Sprintf(" (secrets via tvault:%s)", s.Vault)
					}
					checks = append(checks, checkResult{"server:" + name, true, detail})
				}
			}
			if usesVault {
				if p, err := exec.LookPath("tvault"); err != nil {
					checks = append(checks, checkResult{"tvault", false, "a server uses vault but tvault is not on PATH"})
				} else {
					checks = append(checks, checkResult{"tvault", true, p})
				}
			}

			for _, name := range c.AgentNames() {
				a := c.Agents[name]
				if _, err := harness.For(a.Type); err != nil {
					checks = append(checks, checkResult{"agent:" + name, false, err.Error()})
					continue
				}
				ap := config.ExpandPath(a.Path)
				if _, err := os.Stat(ap); err != nil {
					checks = append(checks, checkResult{"agent:" + name, false, "path not found: " + ap})
				} else {
					checks = append(checks, checkResult{"agent:" + name, true, fmt.Sprintf("%s (%s, %s)", ap, a.Type, a.ResolvedMode())})
				}
			}

			if st, err := openStore(); err != nil {
				checks = append(checks, checkResult{"store", false, err.Error()})
			} else {
				st.Close()
				checks = append(checks, checkResult{"store", true, dbPath()})
			}

			if self, err := os.Executable(); err == nil {
				checks = append(checks, checkResult{"binary", true, self})
			}

			if probe {
				checks = append(checks, probeServers(cmd.Context(), c)...)
			}

			return reportChecks(cmd, checks)
		},
	}
	cmd.Flags().BoolVar(&probe, "probe", false, "actually connect to each enabled server and report its tool count")
	return cmd
}

// probeServers spawns and connects to every enabled server (via the hub) and
// reports, per server, whether the MCP handshake succeeded and how many tools
// it advertised. This is the real connectivity check behind `doctor --probe`.
func probeServers(ctx context.Context, c *config.Config) []checkResult {
	if len(c.EnabledServers()) == 0 {
		return []checkResult{{"probe", true, "no enabled servers to probe"}}
	}
	pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	h := hub.New(c, nil, nil) // no store (don't record), silent logger
	h.Connect(pctx)
	defer h.Close()
	var out []checkResult
	for _, d := range h.Downstreams() {
		if d.Connected() {
			out = append(out, checkResult{"probe:" + d.Name, true, fmt.Sprintf("%d tools", len(d.Tools))})
		} else {
			out = append(out, checkResult{"probe:" + d.Name, false, d.Err.Error()})
		}
	}
	return out
}

func reportChecks(cmd *cobra.Command, checks []checkResult) error {
	if flagJSON {
		return printJSON(cmd, checks)
	}
	out := cmd.OutOrStdout()
	allOK := true
	for _, c := range checks {
		mark := "✔"
		if !c.OK {
			mark = "✗"
			allOK = false
		}
		fmt.Fprintf(out, "%s %-18s %s\n", mark, c.Name, c.Detail)
	}
	if !allOK {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}

func printJSON(cmd *cobra.Command, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}
