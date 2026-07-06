package mcp

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// agentScope is the per-agent curation applied when a gateway is spawned with
// `mcphub mcp serve --agent <name>`. A nil scope (the default, when --agent is
// absent or names an agent with no routing config) means "no restriction": the
// gateway advertises every tool of every connected downstream, exactly as
// before. A non-nil scope limits which servers' tools are advertised and which
// can be reached through the mcphub_* meta-tools.
//
// This is CURATION, not a security boundary: it controls what the gateway
// advertises and honors, not a hard isolation layer. A hostile agent speaking
// the raw MCP protocol is out of scope; the goal is to keep a well-behaved
// agent's context lean and on-task.
type agentScope struct {
	servers map[string]bool // nil = all servers allowed
	tools   map[string]bool // nil = all tools (of allowed servers) allowed
}

// allowsServer reports whether a downstream server is in scope.
func (s *agentScope) allowsServer(name string) bool {
	return s == nil || s.servers == nil || s.servers[name]
}

// allows reports whether a (server, tool) pair is in scope.
func (s *agentScope) allows(server, tool string) bool {
	if s == nil {
		return true
	}
	if !s.allowsServer(server) {
		return false
	}
	if s.tools == nil {
		return true
	}
	return s.tools[server+"__"+tool]
}

// allowsNS reports whether a namespaced `server__tool` name is in scope.
func (s *agentScope) allowsNS(namespaced string) bool {
	if s == nil {
		return true
	}
	i := strings.Index(namespaced, "__")
	if i <= 0 || i == len(namespaced)-2 {
		return false
	}
	return s.allows(namespaced[:i], namespaced[i+2:])
}

// scopeFor builds the agentScope for the given agent name. An empty agentName
// (no --agent flag) yields a nil scope — the unscoped default. An unknown
// agent name is an error so a stale `--agent` arg in a harness file fails fast
// instead of silently serving everything or nothing. An agent with no routing
// config also yields a nil scope (no restriction).
func ScopeFor(cfg *config.Config, agentName string) (*agentScope, error) {
	if agentName == "" {
		return nil, nil
	}
	a, ok := cfg.Agents[agentName]
	if !ok {
		return nil, fmt.Errorf("--agent %q: no such agent in config", agentName)
	}
	sc := &agentScope{}
	if a.Servers != nil {
		sc.servers = make(map[string]bool, len(*a.Servers))
		for _, s := range *a.Servers {
			sc.servers[s] = true
		}
	}
	if toolSet, restricted := a.ToolScope(); restricted {
		sc.tools = toolSet
	}
	if sc.servers == nil && sc.tools == nil {
		return nil, nil // configured agent, no routing -> unscoped
	}
	return sc, nil
}
