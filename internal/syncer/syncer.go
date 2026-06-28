// Package syncer reconciles mcphub.yaml into agent harness configs. It is the
// shared engine behind both `mcphub sync` (CLI) and the Studio TUI's sync
// action, so the two can never drift in behavior.
package syncer

import (
	"context"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

// AgentResult is the outcome of reconciling one agent.
type AgentResult struct {
	Agent   string
	Type    string
	Mode    string
	Skipped bool // agent is disabled
	Plan    harness.Plan
	Err     error
}

// Desired computes the server entries mcphub wants present in an agent: in
// gateway mode a single 'mcphub' server (self), in direct mode every enabled
// downstream server verbatim.
func Desired(c *config.Config, agent config.Agent, self string) []harness.MCPServer {
	if agent.ResolvedMode() == config.ModeGateway {
		return []harness.MCPServer{{
			Name:    "mcphub",
			Command: self,
			Args:    []string{"mcp", "serve"},
		}}
	}
	var out []harness.MCPServer
	for _, name := range c.EnabledServers() {
		s := c.Servers[name]
		// SpawnCommand applies tvault wrapping when the server uses a vault, so
		// a directly-written agent also launches it with secrets injected.
		command, cargs := s.SpawnCommand()
		out = append(out, harness.MCPServer{
			Name:      name,
			Command:   command,
			Args:      cargs,
			Env:       s.Env,
			URL:       s.URL,
			Transport: s.Transport,
		})
	}
	return out
}

// Names returns the names of a desired server set.
func Names(servers []harness.MCPServer) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out
}

// Reconcile plans (and, when write is true, applies) the sync for the given
// agents. An empty agents slice means every agent in the config. A nonexistent
// named agent yields an AgentResult with Err set rather than aborting the run,
// so callers can render a per-agent report. self is the absolute path to the
// mcphub binary (used for gateway entries).
func Reconcile(ctx context.Context, c *config.Config, st *store.Store, self string, agents []string, write bool) []AgentResult {
	targets := agents
	if len(targets) == 0 {
		targets = c.AgentNames()
	}
	results := make([]AgentResult, 0, len(targets))
	for _, name := range targets {
		r := AgentResult{Agent: name}
		agent, ok := c.Agents[name]
		if !ok {
			r.Err = errUnknownAgent(name)
			results = append(results, r)
			continue
		}
		r.Type, r.Mode = agent.Type, string(agent.ResolvedMode())
		if agent.Disabled {
			r.Skipped = true
			results = append(results, r)
			continue
		}
		adapter, err := harness.For(agent.Type)
		if err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		desired := Desired(c, agent, self)
		owned, err := st.ManagedFor(ctx, name)
		if err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		plan, err := adapter.Apply(config.ExpandPath(agent.Path), desired, owned, !write)
		r.Plan, r.Err = plan, err
		if err == nil && write {
			if err := st.SetManaged(ctx, name, Names(desired)); err != nil {
				r.Err = err
			} else {
				_ = st.LogSync(ctx, name, r.Mode, Names(desired), false)
			}
		}
		results = append(results, r)
	}
	return results
}

type unknownAgentError struct{ name string }

func (e unknownAgentError) Error() string { return "no such agent " + e.name }

func errUnknownAgent(name string) error { return unknownAgentError{name} }
