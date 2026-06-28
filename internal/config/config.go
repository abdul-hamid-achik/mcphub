// Package config defines mcphub.yaml — the single source of truth for which
// downstream MCP servers exist, how they group, and which agent harnesses
// mcphub keeps in sync.
//
// The whole point of mcphub is that you edit ONE file (or the Studio TUI) and
// `mcphub sync` propagates the result into every agent harness, so you never
// hand-edit ~/.claude.json, opencode.json, and ~/.codex/config.toml again.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root of mcphub.yaml.
type Config struct {
	Version int                 `yaml:"version"`
	Expose  string              `yaml:"expose,omitempty"` // "all" (default) | "lazy"
	Pin     []string            `yaml:"pin,omitempty"`    // server__tool names always mounted, even in lazy mode
	Servers map[string]Server   `yaml:"servers"`
	Groups  map[string][]string `yaml:"groups,omitempty"`
	Agents  map[string]Agent    `yaml:"agents"`
}

// Exposure controls how many tools the gateway advertises up front.
const (
	// ExposeAll mounts every downstream tool as `server__tool`. Simple, but a
	// large fleet means a large tool list in every agent's context.
	ExposeAll = "all"
	// ExposeLazy advertises only mcphub's meta-tools (list/search/describe/call).
	// The agent finds capabilities with mcphub_search_tools and invokes them via
	// mcphub_call_tool — so the context cost is a handful of tools, not hundreds.
	ExposeLazy = "lazy"
)

// Lazy reports whether the gateway should use on-demand (lazy) tool exposure.
func (c *Config) Lazy() bool { return c.Expose == ExposeLazy }

// PinSet returns the pinned `server__tool` names as a set for O(1) lookup.
func (c *Config) PinSet() map[string]bool {
	out := make(map[string]bool, len(c.Pin))
	for _, p := range c.Pin {
		out[p] = true
	}
	return out
}

// Server describes one downstream MCP server mcphub can manage and proxy.
// Exactly one of Command (stdio) or URL (http/sse) should be set.
type Server struct {
	// Command + Args define a local stdio server (e.g. command: codemap, args: [serve]).
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`

	// URL + Transport define a remote server. Transport is "http" or "sse".
	URL       string `yaml:"url,omitempty"`
	Transport string `yaml:"transport,omitempty"`

	// Vault names a TinyVault (tvault) project. When set, the server is spawned
	// via `tvault run --project <Vault> -- <command>`, so the project's secrets
	// are injected as environment variables at launch and never live in
	// mcphub.yaml. VaultOnly / VaultPrefix narrow the injected keys.
	Vault       string   `yaml:"vault,omitempty"`
	VaultOnly   []string `yaml:"vault_only,omitempty"`
	VaultPrefix string   `yaml:"vault_prefix,omitempty"`

	Enabled     bool     `yaml:"enabled"`
	Description string   `yaml:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
}

// IsRemote reports whether the server is reached over a URL rather than spawned.
func (s Server) IsRemote() bool { return s.URL != "" }

// UsesVault reports whether the server injects secrets through tvault.
func (s Server) UsesVault() bool { return s.Vault != "" }

// SpawnCommand returns the effective command and args used to launch a stdio
// server. When the server references a tvault project, the command is wrapped
// with `tvault run` so the project's secrets are injected as environment
// variables — keeping them out of mcphub.yaml entirely. Remote servers (no
// command) are returned unchanged.
func (s Server) SpawnCommand() (string, []string) {
	if s.Vault == "" || s.Command == "" {
		return s.Command, s.Args
	}
	args := []string{"run", "--project", s.Vault}
	if len(s.VaultOnly) > 0 {
		args = append(args, "--only", strings.Join(s.VaultOnly, ","))
	}
	if s.VaultPrefix != "" {
		args = append(args, "--prefix", s.VaultPrefix)
	}
	args = append(args, "--", s.Command)
	args = append(args, s.Args...)
	return "tvault", args
}

// Mode controls what `mcphub sync` writes into a given agent.
type Mode string

const (
	// ModeGateway writes ONLY the mcphub gateway into the agent, so the agent
	// sees a single MCP server. mcphub proxies all the real servers behind it.
	// This is the token-saving default: one tool list, one connection.
	ModeGateway Mode = "gateway"
	// ModeDirect writes every enabled server straight into the agent's config.
	// Useful for agents you want talking to servers without the gateway hop.
	ModeDirect Mode = "direct"
)

// Agent is one harness mcphub syncs into (claude, opencode, codex, ...).
type Agent struct {
	// Type selects the file-format adapter (claude | opencode | codex | crush).
	Type string `yaml:"type"`
	// Path is the harness config file. Supports ~ expansion.
	Path string `yaml:"path"`
	// Mode is gateway (default) or direct.
	Mode Mode `yaml:"mode,omitempty"`
	// Disabled skips this agent during sync without deleting its definition.
	Disabled bool `yaml:"disabled,omitempty"`
}

// ResolvedMode returns the agent's mode, defaulting to gateway.
func (a Agent) ResolvedMode() Mode {
	if a.Mode == ModeDirect {
		return ModeDirect
	}
	return ModeGateway
}

// DefaultPath returns the path mcphub.yaml is loaded from when unspecified.
// Precedence: $MCPHUB_CONFIG, ./mcphub.yaml, ~/.config/mcphub/mcphub.yaml.
func DefaultPath() string {
	if p := os.Getenv("MCPHUB_CONFIG"); p != "" {
		return p
	}
	if _, err := os.Stat("mcphub.yaml"); err == nil {
		return "mcphub.yaml"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "mcphub.yaml"
	}
	return filepath.Join(home, ".config", "mcphub", "mcphub.yaml")
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Servers == nil {
		c.Servers = map[string]Server{}
	}
	if c.Agents == nil {
		c.Agents = map[string]Agent{}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save validates and writes the config back to path (used by
// `add`/`enable`/`init --from-agents`/Studio). It validates first so an invalid
// config can never be persisted — otherwise the next Load would reject it and
// brick every command until hand-edited. The write is atomic (temp file +
// rename in the same directory) so a crash or full disk can't truncate the
// single source-of-truth file.
func Save(path string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcphub-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Validate checks structural invariants and returns a combined error.
func (c *Config) Validate() error {
	var problems []string
	if c.Expose != "" && c.Expose != ExposeAll && c.Expose != ExposeLazy {
		problems = append(problems, fmt.Sprintf("expose must be %q or %q", ExposeAll, ExposeLazy))
	}
	for name, s := range c.Servers {
		if strings.Contains(name, "__") {
			problems = append(problems, fmt.Sprintf("server %q: name must not contain %q (reserved as the namespacing separator)", name, "__"))
		}
		if s.Command == "" && s.URL == "" {
			problems = append(problems, fmt.Sprintf("server %q: needs either command or url", name))
		}
		if s.Command != "" && s.URL != "" {
			problems = append(problems, fmt.Sprintf("server %q: set command or url, not both", name))
		}
		if s.Transport != "" && s.Transport != "http" && s.Transport != "sse" {
			problems = append(problems, fmt.Sprintf("server %q: transport must be http or sse", name))
		}
		if s.Vault != "" && s.URL != "" {
			problems = append(problems, fmt.Sprintf("server %q: vault injects env into a spawned command and can't be used with a remote url", name))
		}
	}
	for g, members := range c.Groups {
		for _, m := range members {
			if _, ok := c.Servers[m]; !ok {
				problems = append(problems, fmt.Sprintf("group %q references unknown server %q", g, m))
			}
		}
	}
	for _, p := range c.Pin {
		srv, _, ok := strings.Cut(p, "__")
		if !ok {
			problems = append(problems, fmt.Sprintf("pin %q must be a namespaced tool name (server__tool)", p))
			continue
		}
		if _, known := c.Servers[srv]; !known {
			problems = append(problems, fmt.Sprintf("pin %q references unknown server %q", p, srv))
		}
	}
	for name, a := range c.Agents {
		if a.Path == "" {
			problems = append(problems, fmt.Sprintf("agent %q: missing path", name))
		}
		if a.Type == "" {
			problems = append(problems, fmt.Sprintf("agent %q: missing type", name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// EnabledServers returns the names of enabled servers, sorted.
func (c *Config) EnabledServers() []string {
	var out []string
	for name, s := range c.Servers {
		if s.Enabled {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ServerNames returns all server names, sorted.
func (c *Config) ServerNames() []string {
	out := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AgentNames returns all agent names, sorted.
func (c *Config) AgentNames() []string {
	out := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ExpandPath expands a leading ~ to the user's home directory.
func ExpandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
