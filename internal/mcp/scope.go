package mcp

import (
	"fmt"
	"sort"
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
	servers          map[string]bool // nil = all servers allowed
	tools            map[string]bool // nil = all tools (of allowed servers) allowed
	pin              *[]string       // nil = inherit global pins; non-nil empty = no direct downstream pins
	toolSchemaBudget *int            // nil = unlimited historical behavior; 0 = meta-tools only
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

// allowedToolNames returns the exact tool names allowed for server when this
// scope has a tool allowlist. The second result is false when tools are not
// restricted, which is distinct from an explicit empty allowlist.
func (s *agentScope) allowedToolNames(server string) ([]string, bool) {
	if s == nil || s.tools == nil {
		return nil, false
	}
	if !s.allowsServer(server) {
		return []string{}, true
	}
	prefix := server + "__"
	tools := make([]string, 0)
	for namespaced, allowed := range s.tools {
		if allowed && strings.HasPrefix(namespaced, prefix) && s.allowsNS(namespaced) {
			tools = append(tools, strings.TrimPrefix(namespaced, prefix))
		}
	}
	sort.Strings(tools)
	return tools, true
}

// configuredPins returns the pin list this gateway agent selected. A nil scope
// or omitted per-agent override inherits the top-level list. Returning the
// configured slice directly is safe because callers never mutate it.
func (s *agentScope) configuredPins(cfg *config.Config) []string {
	if s != nil && s.pin != nil {
		return *s.pin
	}
	if cfg == nil {
		return nil
	}
	return cfg.Pin
}

// pinMatches reports whether a directly advertised downstream tool matches
// this agent's effective pins. This policy is deliberately separate from
// allows: a tool can remain callable through mcphub_call_tool without spending
// context on a direct definition.
func (s *agentScope) pinMatches(cfg *config.Config, namespaced string) bool {
	return config.PinListMatches(s.configuredPins(cfg), namespaced)
}

// schemaBudget returns the optional serialized downstream tool-definition
// budget for this agent.
func (s *agentScope) schemaBudget() (int, bool) {
	if s == nil || s.toolSchemaBudget == nil {
		return 0, false
	}
	return *s.toolSchemaBudget, true
}

// effectivePins projects this agent's configured pins (override or inherited
// globals) into its call scope. Exact tool scopes expand a whole-server pin
// into the exact names that can really mount.
func (s *agentScope) effectivePins(cfg *config.Config) []string {
	if cfg == nil {
		return []string{}
	}
	configured := s.configuredPins(cfg)
	if s == nil {
		return append([]string(nil), configured...)
	}
	if s.tools == nil {
		pins := make([]string, 0, len(configured))
		for _, pin := range configured {
			if s.allowsServer(config.PinServer(pin)) {
				pins = append(pins, pin)
			}
		}
		return pins
	}
	pins := make([]string, 0, len(s.tools))
	for namespaced, allowed := range s.tools {
		if allowed && s.allowsNS(namespaced) && s.pinMatches(cfg, namespaced) {
			pins = append(pins, namespaced)
		}
	}
	sort.Strings(pins)
	return pins
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
	if a.Pin != nil {
		sc.pin = a.Pin
	}
	if budget, configured := a.ToolSchemaBudgetBytes(); configured {
		sc.toolSchemaBudget = &budget
	}
	if sc.servers == nil && sc.tools == nil && sc.pin == nil && sc.toolSchemaBudget == nil {
		return nil, nil // configured agent, no routing -> unscoped
	}
	return sc, nil
}
