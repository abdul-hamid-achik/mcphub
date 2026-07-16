package cli

import (
	"context"
	"fmt"
	"os/exec"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/syncer"
)

// serverAgentState is one agent's wiring state scoped to a single server —
// the per-agent slice surfaced by `doctor --server` and `status --server`.
type serverAgentState struct {
	Agent string `json:"agent"`
	Mode  string `json:"mode"`
	State string `json:"state"` // "in sync" | "N pending" | "disabled" | "error"
}

// scopedServerReport is the single-server view behind `doctor --server <name>`
// and `status --server <name>`: registration, enabled state, PATH availability,
// the agents that route to it, and how many calls the gateway has proxied to
// it. HandshakeOK/ToolCount are only set when probing; ProbeError is set when a
// probe was attempted but the server failed to connect.
type scopedServerReport struct {
	Server       string             `json:"server"`
	Registered   bool               `json:"registered"`
	Enabled      bool               `json:"enabled"`
	OnPath       bool               `json:"on_path"`
	Remote       bool               `json:"remote,omitempty"`
	HandshakeOK  *bool              `json:"handshake_ok,omitempty"`
	ToolCount    *int               `json:"tool_count,omitempty"`
	ProbeError   string             `json:"probe_error,omitempty"`
	Agents       []serverAgentState `json:"agents"`
	ProxiedCalls int64              `json:"proxied_calls"`
	Unused       bool               `json:"unused,omitempty"` // enabled but never proxied
}

// buildScopedServerReport assembles the single-server view. When probe is true
// it also spawns and connects to the server (via the hub) and fills
// HandshakeOK/ToolCount. st may be nil to skip the proxied-calls lookup.
func buildScopedServerReport(ctx context.Context, c *config.Config, st *store.Store, name string, probe bool) scopedServerReport {
	rep := scopedServerReport{Server: name, Agents: []serverAgentState{}}
	s, ok := c.Servers[name]
	if !ok {
		return rep
	}
	rep.Registered = true
	rep.Enabled = s.Enabled
	if s.IsRemote() {
		rep.Remote = true
		rep.OnPath = true // remote servers need no binary on PATH
	} else if _, err := exec.LookPath(s.Command); err == nil {
		rep.OnPath = true
	}

	if st != nil {
		if stats, err := st.ServerStats(ctx); err == nil {
			for _, row := range stats {
				if row.Server == name {
					rep.ProxiedCalls = row.Calls
					break
				}
			}
		}
	}
	if rep.Enabled && rep.ProxiedCalls == 0 {
		rep.Unused = true
	}

	self, _ := syncer.Self()
	results := syncer.Reconcile(ctx, c, st, self, nil, false)
	for _, r := range results {
		if !rep.Enabled || !agentRoutesTo(c.Agents[r.Agent], name) {
			continue
		}
		as := serverAgentState{Agent: r.Agent, Mode: r.Mode}
		switch {
		case r.Err != nil:
			as.State = "error"
		case r.Skipped:
			as.State = "disabled"
		case r.Plan.HasChanges():
			as.State = fmt.Sprintf("%d pending", countChanges(r.Plan))
		default:
			as.State = "in sync"
		}
		rep.Agents = append(rep.Agents, as)
	}

	if probe && rep.Enabled {
		probeOneServer(ctx, c, name, &rep)
	}
	return rep
}

// agentRoutesTo reports whether the agent's server scope includes `name`. An
// unscoped agent (Servers nil) reaches every enabled server; a scoped one
// reaches only its listed servers. Callers must gate on the server being
// enabled before treating this as "actually routed".
func agentRoutesTo(a config.Agent, name string) bool {
	if a.Servers == nil {
		return true
	}
	for _, s := range *a.Servers {
		if s == name {
			return true
		}
	}
	return false
}

// probeOneServer spawns and connects to a single enabled server (via the hub)
// and records whether the MCP handshake succeeded and how many tools it
// advertised. Disabled or unknown servers are left untouched (nil fields).
func probeOneServer(ctx context.Context, c *config.Config, name string, rep *scopedServerReport) {
	pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	h := hub.New(c, nil, nil) // no store (don't record), silent logger
	h.Connect(pctx)
	defer h.Close()
	for _, d := range h.Downstreams() {
		if d.Name != name {
			continue
		}
		ok := d.Connected()
		rep.HandshakeOK = &ok
		if ok {
			n := len(d.Tools)
			rep.ToolCount = &n
		} else if d.Err != nil {
			rep.ProbeError = d.Err.Error()
		}
		return
	}
}

// renderScopedServerReport prints the single-server view in a compact
// human-readable form (the --json path emits the struct verbatim).
func renderScopedServerReport(cmd *cobra.Command, rep scopedServerReport) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Server:  %s\n", rep.Server)
	if !rep.Registered {
		fmt.Fprintf(out, "  not registered in mcphub.yaml\n")
		return nil
	}
	fmt.Fprintf(out, "  registered: %t   enabled: %t   on_path: %t", rep.Registered, rep.Enabled, rep.OnPath)
	if rep.Remote {
		fmt.Fprintf(out, "   remote: true")
	}
	fmt.Fprintln(out)
	if rep.HandshakeOK != nil {
		if *rep.HandshakeOK {
			fmt.Fprintf(out, "  handshake: ok   tools: %d\n", *rep.ToolCount)
		} else {
			detail := "failed"
			if rep.ProbeError != "" {
				detail = "failed: " + rep.ProbeError
			}
			fmt.Fprintf(out, "  handshake: %s\n", detail)
		}
	}
	fmt.Fprintf(out, "  proxied_calls: %d\n", rep.ProxiedCalls)
	if rep.Unused {
		fmt.Fprintf(out, "  unused (enabled but never proxied) — consider `mcphub disable %s`.\n", rep.Server)
	}
	if len(rep.Agents) == 0 {
		fmt.Fprintf(out, "  agents: none route to %s", rep.Server)
		if !rep.Enabled {
			fmt.Fprintf(out, " (server is disabled)")
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintf(out, "  agents (%d):\n", len(rep.Agents))
		w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "    AGENT\tMODE\tSYNC")
		for _, a := range rep.Agents {
			fmt.Fprintf(w, "    %s\t%s\t%s\n", a.Agent, a.Mode, a.State)
		}
		w.Flush()
	}
	return nil
}
