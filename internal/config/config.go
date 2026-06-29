// Package config defines mcphub.yaml — the single source of truth for which
// downstream MCP servers exist, how they group, and which agent harnesses
// mcphub keeps in sync.
//
// The whole point of mcphub is that you edit ONE file (or the Studio TUI) and
// `mcphub sync` propagates the result into every agent harness, so you never
// hand-edit ~/.claude.json, opencode.json, and ~/.codex/config.toml again.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// configNames lists the base filenames Load looks for, in precedence order.
var configNames = []string{"mcphub.yaml", "mcphub.yml", "mcphub.toml", "mcphub.json"}

// formatOf returns the serialization format implied by a file extension.
// Anything that isn't .toml or .json is treated as YAML.
func formatOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		return "toml"
	case ".json":
		return "json"
	default:
		return "yaml"
	}
}

func unmarshalConfig(body []byte, format string) (*Config, error) {
	var c Config
	var err error
	switch format {
	case "toml":
		err = toml.Unmarshal(body, &c)
	case "json":
		err = json.Unmarshal(body, &c)
	default:
		err = yaml.Unmarshal(body, &c)
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func marshalConfig(c *Config, format string) ([]byte, error) {
	switch format {
	case "toml":
		return toml.Marshal(c)
	case "json":
		b, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	default:
		return yaml.Marshal(c)
	}
}

// Config is the root of the mcphub config file (YAML, TOML, or JSON — see Load).
type Config struct {
	Version        int                 `yaml:"version" toml:"version" json:"version"`
	Expose         string              `yaml:"expose,omitempty" toml:"expose,omitempty" json:"expose,omitempty"` // "all" (default) | "lazy"
	Pin            []string            `yaml:"pin,omitempty" toml:"pin,omitempty" json:"pin,omitempty"`          // server__tool names always mounted, even in lazy mode
	Servers        map[string]Server   `yaml:"servers" toml:"servers" json:"servers"`
	Groups         map[string][]string `yaml:"groups,omitempty" toml:"groups,omitempty" json:"groups,omitempty"`
	Agents         map[string]Agent    `yaml:"agents" toml:"agents" json:"agents"`
	ConnectTimeout string              `yaml:"connect_timeout,omitempty" toml:"connect_timeout,omitempty" json:"connect_timeout,omitempty"` // per-downstream connect timeout, e.g. "30s", "60s" (default 30s)
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

// PinMatches reports whether a namespaced `server__tool` name is pinned. A pin
// entry may be an exact `server__tool`, a bare `server` (pins all of that
// server's tools), or a `server__*` wildcard (same as the bare form).
func (c *Config) PinMatches(namespaced string) bool {
	server := namespaced
	if i := strings.Index(namespaced, "__"); i >= 0 {
		server = namespaced[:i]
	}
	for _, p := range c.Pin {
		switch p {
		case namespaced, server, server + "__*":
			return true
		}
	}
	return false
}

// PinServer extracts the server name a pin entry refers to (the part before the
// first `__`, with a trailing `__*` wildcard stripped).
func PinServer(p string) string {
	p = strings.TrimSuffix(p, "__*")
	if i := strings.Index(p, "__"); i >= 0 {
		return p[:i]
	}
	return p
}

// ServerPinned reports whether any pin entry resolves to the given bare server
// name (a bare `server`, a `server__*` wildcard, or any `server__tool`).
func (c *Config) ServerPinned(name string) bool {
	for _, p := range c.Pin {
		if PinServer(p) == name {
			return true
		}
	}
	return false
}

// UnpinServer removes every pin entry (bare, wildcard, or exact tool) that
// resolves to the given server name.
func (c *Config) UnpinServer(name string) {
	kept := c.Pin[:0]
	for _, p := range c.Pin {
		if PinServer(p) != name {
			kept = append(kept, p)
		}
	}
	c.Pin = kept
}

// Server describes one downstream MCP server mcphub can manage and proxy.
// Exactly one of Command (stdio) or URL (http/sse) should be set.
type Server struct {
	// Command + Args define a local stdio server (e.g. command: codemap, args: [serve]).
	Command string            `yaml:"command,omitempty" toml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" toml:"args,omitempty" json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" toml:"env,omitempty" json:"env,omitempty"`

	// URL + Transport define a remote server. Transport is "http" or "sse".
	URL       string `yaml:"url,omitempty" toml:"url,omitempty" json:"url,omitempty"`
	Transport string `yaml:"transport,omitempty" toml:"transport,omitempty" json:"transport,omitempty"`

	// Vault names a TinyVault (tvault) project. When set, the server is spawned
	// via `tvault run --project <Vault> -- <command>`, so the project's secrets
	// are injected as environment variables at launch and never live in the
	// config file. VaultOnly / VaultPrefix narrow the injected keys.
	Vault       string   `yaml:"vault,omitempty" toml:"vault,omitempty" json:"vault,omitempty"`
	VaultOnly   []string `yaml:"vault_only,omitempty" toml:"vault_only,omitempty" json:"vault_only,omitempty"`
	VaultPrefix string   `yaml:"vault_prefix,omitempty" toml:"vault_prefix,omitempty" json:"vault_prefix,omitempty"`

	Enabled     bool     `yaml:"enabled" toml:"enabled" json:"enabled"`
	Description string   `yaml:"description,omitempty" toml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" toml:"tags,omitempty" json:"tags,omitempty"`
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
	// Type selects the file-format adapter (claude | opencode | codex | crush | forge | hermes).
	Type string `yaml:"type" toml:"type" json:"type"`
	// Path is the harness config file. Supports ~ expansion.
	Path string `yaml:"path" toml:"path" json:"path"`
	// Mode is gateway (default) or direct.
	Mode Mode `yaml:"mode,omitempty" toml:"mode,omitempty" json:"mode,omitempty"`
	// Disabled skips this agent during sync without deleting its definition.
	Disabled bool `yaml:"disabled,omitempty" toml:"disabled,omitempty" json:"disabled,omitempty"`
}

// ResolvedMode returns the agent's mode, defaulting to gateway.
func (a Agent) ResolvedMode() Mode {
	if a.Mode == ModeDirect {
		return ModeDirect
	}
	return ModeGateway
}

// validAgentTypes lists every harness type harness.For accepts. Kept in sync
// with internal/harness.For — if you add a harness, add its type here too.
var validAgentTypes = map[string]bool{
	"claude": true, "claudecode": true,
	"opencode": true,
	"codex":    true,
	"crush":    true,
	"forge":    true, "forgecode": true,
	"hermes": true,
}

// DefaultPath returns the config path used when unspecified. Precedence:
// $MCPHUB_CONFIG, then the first existing mcphub.{yaml,yml,toml,json} in the
// current directory, then in ~/.config/mcphub, else ~/.config/mcphub/mcphub.yaml.
func DefaultPath() string {
	if p := os.Getenv("MCPHUB_CONFIG"); p != "" {
		return p
	}
	for _, name := range configNames {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "mcphub.yaml"
	}
	dir := filepath.Join(home, ".config", "mcphub")
	for _, name := range configNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(dir, "mcphub.yaml")
}

// Load reads and validates a config file. The format is chosen from the file
// extension: .toml, .json, or YAML for everything else.
func Load(path string) (*Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c, err := unmarshalConfig(body, formatOf(path))
	if err != nil {
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
	return c, nil
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
	body, err := marshalConfig(c, formatOf(path))
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcphub-*.tmp")
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
	// Preserve the existing file's permissions, or default to 0o600 for a new
	// config (it may carry env vars / vault references).
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Starter returns the default seed config used by `mcphub init`. It is the
// structured form of the commented YAML starter, so init can emit it as TOML or
// JSON too.
func Starter() *Config {
	return &Config{
		Version: 1,
		Expose:  ExposeAll,
		Servers: map[string]Server{
			"codemap":    {Command: "codemap", Args: []string{"serve"}, Enabled: true, Description: "Code knowledge graph", Tags: []string{"code", "search"}},
			"vecgrep":    {Command: "vecgrep", Args: []string{"serve", "--mcp"}, Enabled: true, Description: "Semantic code search", Tags: []string{"code", "search"}},
			"monitor":    {Command: "monitor", Args: []string{"mcp", "serve"}, Enabled: true, Description: "Local system & process observability", Tags: []string{"ops"}},
			"cairntrace": {Command: "cairn", Args: []string{"mcp"}, Enabled: false, Description: "Service discovery, audit & investigation", Tags: []string{"ops"}},
			"glyph":      {Command: "glyph", Args: []string{"mcp"}, Enabled: false, Description: "TUI behavior testing"},
		},
		Groups: map[string][]string{
			"coding": {"codemap", "vecgrep"},
			"ops":    {"monitor", "cairntrace"},
		},
		Agents: map[string]Agent{
			"claude":   {Type: "claude", Path: "~/.claude.json", Mode: ModeGateway},
			"opencode": {Type: "opencode", Path: "~/.config/opencode/opencode.json", Mode: ModeDirect},
			"codex":    {Type: "codex", Path: "~/.codex/config.toml", Mode: ModeGateway},
			"crush":    {Type: "crush", Path: "~/.config/crush/crush.json", Mode: ModeGateway},
			"forge":    {Type: "forge", Path: "~/forge/.mcp.json", Mode: ModeGateway},
			"hermes":   {Type: "hermes", Path: "~/.hermes/config.yaml", Mode: ModeGateway},
		},
	}
}

// Validate checks structural invariants and returns a combined error.
func (c *Config) Validate() error {
	var problems []string
	if c.Expose != "" && c.Expose != ExposeAll && c.Expose != ExposeLazy {
		problems = append(problems, fmt.Sprintf("expose must be %q or %q", ExposeAll, ExposeLazy))
	}
	for name, s := range c.Servers {
		if name == "mcphub" {
			problems = append(problems, `server "mcphub": name is reserved for the gateway entry written into agents`)
		}
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
		if p == "" {
			problems = append(problems, "pin entries must not be empty")
			continue
		}
		srv := PinServer(p)
		if _, known := c.Servers[srv]; !known {
			problems = append(problems, fmt.Sprintf("pin %q references unknown server %q", p, srv))
			continue
		}
		// Only exact `server__tool`, bare `server`, or `server__*` are matched by
		// PinMatches — reject other wildcards or a trailing `__`, which would
		// validate but silently pin nothing.
		if strings.Contains(p, "*") && p != srv+"__*" {
			problems = append(problems, fmt.Sprintf("pin %q: only whole-server wildcards (%s__*) are supported", p, srv))
		}
		if strings.HasSuffix(p, "__") {
			problems = append(problems, fmt.Sprintf("pin %q: trailing %q matches no tool; use %q or %s__* instead", p, "__", srv, srv))
		}
	}
	for name, a := range c.Agents {
		if a.Path == "" {
			problems = append(problems, fmt.Sprintf("agent %q: missing path", name))
		}
		if a.Type == "" {
			problems = append(problems, fmt.Sprintf("agent %q: missing type", name))
		} else if !validAgentTypes[a.Type] {
			problems = append(problems, fmt.Sprintf("agent %q: unknown type %q (supported: claude, opencode, codex, crush, forge, hermes)", name, a.Type))
		}
	}
	if c.ConnectTimeout != "" {
		if _, err := time.ParseDuration(c.ConnectTimeout); err != nil {
			problems = append(problems, fmt.Sprintf("connect_timeout %q: %v (try 30s, 60s, 2m)", c.ConnectTimeout, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// ConnectTimeoutDuration returns the configured per-downstream connect timeout,
// defaulting to 30s when unset.
func (c *Config) ConnectTimeoutDuration() time.Duration {
	if c.ConnectTimeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(c.ConnectTimeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
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
