package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
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
  monitor:
    command: monitor
    args: [mcp, serve]
    enabled: true
    description: Local system & process observability
    tags: [ops]
  cairntrace:
    command: cairn
    args: [mcp]
    enabled: false
    description: Service discovery, audit & investigation
    tags: [ops]
  glyph:
    command: glyph
    args: [mcp]
    enabled: false
    description: TUI behavior testing

# Optional named bundles you can enable together with 'mcphub use <group>'.
groups:
  coding: [codemap, vecgrep]
  ops: [monitor, cairntrace]

# Agent harnesses mcphub keeps in sync. Paths follow each tool's convention;
# XDG-based ones live under ~/.config (or $XDG_CONFIG_HOME if you set it).
#   mode: gateway  -> the agent sees ONLY mcphub, which proxies the rest (saves tokens)
#   mode: direct   -> every enabled server is written straight into the agent
agents:
  claude:
    type: claude
    path: ~/.claude.json
    mode: gateway
  opencode:
    type: opencode
    path: ~/.config/opencode/opencode.json   # XDG
    mode: direct
  codex:
    type: codex
    path: ~/.codex/config.toml
    mode: gateway
  crush:
    type: crush
    path: ~/.config/crush/crush.json          # XDG
    mode: gateway
  forge:
    type: forge
    path: ~/forge/.mcp.json
    mode: gateway
  hermes:
    type: hermes
    path: ~/.hermes/config.yaml
    mode: gateway
# Per-agent routing (optional): restrict which servers/tools an agent sees.
#   servers: [a, b]   only those enabled servers (gateway proxies just them;
#                     direct writes just them). Omit = all enabled; [] = none.
#   tools: [a__x]     gateway-only: only those server__tool names are advertised.
#                     Omit = all tools of the allowed servers; [] = none.
# Example: a token-frugal coding agent that only gets codemap's find + vecgrep search:
#   claude:
#     type: claude
#     path: ~/.claude.json
#     mode: gateway
#     servers: [codemap, vecgrep]
#     tools: [codemap__codemap_find, vecgrep__vecgrep_search]

# Additional agent harnesses are supported but not seeded by default — add them
# when you install the tool:
#   copilot  ~/.copilot/mcp-config.json      GitHub Copilot CLI
#   qwen     ~/.qwen/settings.json            Qwen Code
#   gemini   ~/.gemini/settings.json          Gemini CLI
#   kilo     ~/.config/kilo/kilo.jsonc        Kilo Code (XDG)
#   kimi     ~/.kimi/config.toml               Kimi Code CLI
#
# Run 'mcphub init --from-agents' to auto-discover installed agents, or
# 'mcphub agents' to see all supported types and their status.
`

func newInitCmd() *cobra.Command {
	var force, fromAgents bool
	var format string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter config (yaml, toml, or json)",
		Long: `init creates a starter config. The config can be YAML (default), TOML, or
JSON — pick with --format; mcphub reads and writes all three.

By default it writes a small starter config. With --from-agents it scans your
installed harness configs (Claude Code, opencode, Codex, Crush, Forge, Hermes),
unions every MCP server they already declare, and wires those agents up in
gateway mode — so you can adopt mcphub without retyping what you already have.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := configPath()
			if format != "" {
				ext, err := extForFormat(format)
				if err != nil {
					return err
				}
				path = strings.TrimSuffix(path, filepath.Ext(path)) + ext
			}
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
			// YAML keeps the nicely-commented starter; TOML/JSON are serialized
			// from the structured default (no inline comments in those formats).
			if filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" || filepath.Ext(path) == "" {
				if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
					return err
				}
			} else if err := config.Save(path, config.Starter()); err != nil {
				return err
			}
			fmt.Fprintf(out, "Wrote %s\nNext: edit it, then `mcphub sync` (dry-run) to preview.\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().BoolVar(&fromAgents, "from-agents", false, "import servers from your installed harness configs")
	cmd.Flags().StringVar(&format, "format", "", "config format: yaml (default), toml, or json")
	return cmd
}

// extForFormat maps a --format value to a file extension.
func extForFormat(format string) (string, error) {
	switch strings.ToLower(format) {
	case "yaml", "yml":
		return ".yaml", nil
	case "toml":
		return ".toml", nil
	case "json":
		return ".json", nil
	default:
		return "", fmt.Errorf("unknown --format %q (yaml, toml, or json)", format)
	}
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
	var since, server string
	var markdown bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show local tool-call intelligence",
		Long: `stats summarizes the tool calls the gateway has recorded.

By default it shows all-time totals and a per-server breakdown. Use --tools for
a per-tool breakdown (which exact tools cost the most), --recent N to list the
most recent N calls, --since to limit to a recent window (e.g. --since 24h,
--since 7d), or --server to drill into one server's tools — handy for "which
servers are earning their keep lately".`,
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
			// --server filter: drill into one server's stats.
			if server != "" {
				servers = filterServerStats(servers, server)
				tools = filterToolStats(tools, server)
				recents = filterRecentCalls(recents, server)
				totals = store.Totals{}
				for _, s := range servers {
					totals.Calls += s.Calls
					totals.Errors += s.Errors
					totals.EstTokens += s.EstTokens
					totals.TotalMs += s.AvgMs * s.Calls
				}
			}
			if flagJSON {
				return printJSON(cmd, map[string]any{"totals": totals, "servers": servers, "tools": tools, "recent": recents})
			}
			scope := "all time"
			if window > 0 {
				scope = "last " + since
			}
			if markdown {
				return renderStatsMarkdown(cmd, scope, totals, servers, tools, byTools)
			}
			out := cmd.OutOrStdout()
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
	cmd.Flags().BoolVar(&markdown, "markdown", false, "render as Markdown (great for notes/issues)")
	cmd.Flags().StringVar(&server, "server", "", "filter to one server's stats and tools")
	return cmd
}

func renderStatsMarkdown(cmd *cobra.Command, scope string, totals store.Totals, servers []store.ServerStat, tools []store.ToolStat, byTools bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# mcphub stats\n\n")
	fmt.Fprintf(out, "**Totals (%s):** %d calls · %d errors · ~%d est. tokens · %dms total\n\n",
		scope, totals.Calls, totals.Errors, totals.EstTokens, totals.TotalMs)
	if totals.Calls == 0 {
		fmt.Fprintln(out, "_No tool calls recorded for this window._")
		return nil
	}
	if byTools {
		fmt.Fprintf(out, "| Server | Tool | Calls | Errors | Avg ms | Est tokens |\n| --- | --- | ---: | ---: | ---: | ---: |\n")
		for _, t := range tools {
			fmt.Fprintf(out, "| %s | %s | %d | %d | %d | %d |\n", t.Server, t.Tool, t.Calls, t.Errors, t.AvgMs, t.EstTokens)
		}
	} else {
		fmt.Fprintf(out, "| Server | Calls | Errors | Avg ms | Est tokens |\n| --- | ---: | ---: | ---: | ---: |\n")
		for _, s := range servers {
			fmt.Fprintf(out, "| %s | %d | %d | %d | %d |\n", s.Server, s.Calls, s.Errors, s.AvgMs, s.EstTokens)
		}
	}
	return nil
}

func filterServerStats(rows []store.ServerStat, server string) []store.ServerStat {
	var out []store.ServerStat
	for _, s := range rows {
		if s.Server == server {
			out = append(out, s)
		}
	}
	return out
}

func filterToolStats(rows []store.ToolStat, server string) []store.ToolStat {
	var out []store.ToolStat
	for _, t := range rows {
		if t.Server == server {
			out = append(out, t)
		}
	}
	return out
}

func filterRecentCalls(rows []recentCall, server string) []recentCall {
	prefix := server + "__"
	var out []recentCall
	for _, r := range rows {
		if strings.HasPrefix(r.Namespaced, prefix) {
			out = append(out, r)
		}
	}
	return out
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
	var server string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose config, server availability, and agent targets",
		Long: `doctor checks that your config parses, every enabled server's command is
on PATH, each agent target exists, and the intelligence store opens.

With --probe it goes further: it actually spawns each enabled server, performs
the MCP handshake, and reports how many tools each one exposes (or why it
failed) — a real connectivity check, not just a PATH lookup.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}

			// Scoped single-server view: a cheap "am I wired in?" answer for
			// one server, used by downstream tools like Cortex.
			if server != "" {
				st, err := openStore()
				if err != nil {
					return err
				}
				defer st.Close()
				rep := buildScopedServerReport(cmd.Context(), c, st, server, probe)
				if flagJSON {
					return printJSON(cmd, rep)
				}
				return renderScopedServerReport(cmd, rep)
			}

			var checks []checkResult
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
				if a.HasRouting() {
					allowed := a.AllowedServers(c.EnabledServers())
					routingDetail := fmt.Sprintf("routes to %d/%d enabled servers", len(allowed), len(c.EnabledServers()))
					if a.Tools != nil && len(*a.Tools) > 0 {
						routingDetail += fmt.Sprintf(", %d tools", len(*a.Tools))
					}
					checks = append(checks, checkResult{"agent:" + name + ":routing", true, routingDetail})
					if a.Servers != nil {
						for _, s := range *a.Servers {
							if srv, ok := c.Servers[s]; ok && !srv.Enabled {
								checks = append(checks, checkResult{"agent:" + name + ":routing", false, "server " + s + " is listed but disabled — enable it or drop it from servers"})
							}
						}
					}
				}
			}
			configuredTypes := map[string]bool{}
			for _, name := range c.AgentNames() {
				configuredTypes[c.Agents[name].Type] = true
			}
			for _, spec := range defaultAgentSpecs() {
				if configuredTypes[spec.Type] {
					continue
				}
				if _, err := os.Stat(config.ExpandPath(spec.Path)); err == nil {
					checks = append(checks, checkResult{"available:" + spec.Type, true, "config file exists but not in mcphub.yaml — add it or run 'mcphub init --from-agents'"})
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
	cmd.Flags().StringVar(&server, "server", "", "scope to one server: a single-server registration/routing/usage summary")
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
