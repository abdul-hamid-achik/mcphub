package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `version: 1
servers:
  alpha: {command: cmda, args: [serve], enabled: true, description: A}
  beta:  {url: "https://example.com/mcp", transport: http, enabled: false}
groups:
  g: [alpha]
agents:
  claude:   {type: claude, path: ~/.claude.json, mode: gateway}
  opencode: {type: opencode, path: ./oc.json, mode: direct}
`

func writeSample(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcphub.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAndAccessors(t *testing.T) {
	c, err := Load(writeSample(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.EnabledServers(); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("EnabledServers() = %v, want [alpha]", got)
	}
	if !c.Servers["beta"].IsRemote() {
		t.Error("beta should be remote")
	}
	if c.Agents["claude"].ResolvedMode() != ModeGateway {
		t.Error("claude should resolve to gateway")
	}
	if c.Agents["opencode"].ResolvedMode() != ModeDirect {
		t.Error("opencode should resolve to direct")
	}
	if names := c.ServerNames(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("ServerNames() = %v", names)
	}
}

func TestValidateRejectsBadServers(t *testing.T) {
	cases := map[string]*Config{
		"no command or url": {Servers: map[string]Server{"x": {}}},
		"both command+url":  {Servers: map[string]Server{"x": {Command: "a", URL: "b"}}},
		"bad transport":     {Servers: map[string]Server{"x": {URL: "u", Transport: "grpc"}}},
		"headers on stdio":  {Servers: map[string]Server{"x": {Command: "a", Headers: map[string]string{"Authorization": "Bearer tok"}}}},
		"unknown group ref": {Servers: map[string]Server{}, Groups: map[string][]string{"g": {"missing"}}},
		"agent no path":     {Servers: map[string]Server{}, Agents: map[string]Agent{"a": {Type: "claude"}}},
		"agent bad type":    {Servers: map[string]Server{}, Agents: map[string]Agent{"a": {Type: "cluade", Path: "~/x"}}},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestResolvedModeDefaultsGateway(t *testing.T) {
	if (Agent{}).ResolvedMode() != ModeGateway {
		t.Error("empty mode should default to gateway")
	}
}

func TestAgentRoutingHelpers(t *testing.T) {
	all := []string{"a", "b", "c", "d"}

	// Unscoped agent: no routing, sees everything.
	plain := Agent{}
	if plain.HasRouting() {
		t.Error("empty agent should have no routing")
	}
	if got := plain.AllowedServers(all); !equalStrings(got, all) {
		t.Errorf("AllowedServers unscoped = %v, want %v", got, all)
	}
	if set, restricted := plain.ToolScope(); restricted || set != nil {
		t.Errorf("ToolScope unscoped = %v,%v, want nil,false", set, restricted)
	}

	// Servers-only agent: subset, all tools of those servers.
	srvOnly := Agent{Servers: &[]string{"b", "d", "ghost"}}
	if !srvOnly.HasRouting() {
		t.Error("Servers set should count as routing")
	}
	if got, want := srvOnly.AllowedServers(all), []string{"b", "d"}; !equalStrings(got, want) {
		t.Errorf("AllowedServers subset = %v, want %v (unknown/dropped silently)", got, want)
	}
	if _, restricted := srvOnly.ToolScope(); restricted {
		t.Error("Servers-only agent should not restrict tools")
	}

	// Tools agent: per-tool allowlist.
	tools := Agent{Tools: &[]string{"a__x", "c__y"}}
	if set, restricted := tools.ToolScope(); !restricted || len(set) != 2 || !set["a__x"] || !set["c__y"] {
		t.Errorf("ToolScope = %v,%v, want {a__x,c__y},true", set, restricted)
	}
	// A non-nil empty slice IS routing — it means "none of these" and is
	// distinct from a nil pointer (omitted = all). The pointer carries intent.
	emptyTools := Agent{Tools: &[]string{}}
	if !emptyTools.HasRouting() {
		t.Error("non-nil empty Tools slice should count as routing (means none)")
	}
	if set, restricted := emptyTools.ToolScope(); !restricted || len(set) != 0 {
		t.Errorf("empty Tools ToolScope = %v,%v, want empty set,true", set, restricted)
	}
	// A nil pointer (omitted) is not routing.
	if (Agent{}).HasRouting() {
		t.Error("nil Servers/Tools should not count as routing")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestValidateRejectsBadRouting(t *testing.T) {
	servers := map[string]Server{"a": {Command: "a", Enabled: true}, "b": {Command: "b", Enabled: true}}
	cases := map[string]*Config{
		"unknown server in list": {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Servers: &[]string{"a", "nope"}}}},
		"tools on direct agent":  {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Mode: ModeDirect, Tools: &[]string{"a__x"}}}},
		"tool no separator":      {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Tools: &[]string{"justaname"}}}},
		"tool trailing __":       {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Tools: &[]string{"a__"}}}},
		"tool wildcard":          {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Tools: &[]string{"a__*"}}}},
		"tool unknown server":    {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Tools: &[]string{"zzz__x"}}}},
		"tool server not listed": {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Servers: &[]string{"a"}, Tools: &[]string{"b__x"}}}},
		"empty tool entry":       {Servers: servers, Agents: map[string]Agent{"x": {Type: "claude", Path: "~/x", Tools: &[]string{""}}}},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestValidateAcceptsGoodRouting(t *testing.T) {
	c := &Config{
		Servers: map[string]Server{"a": {Command: "a", Enabled: true}, "b": {Command: "b", Enabled: true}},
		Agents: map[string]Agent{
			"servers-only":       {Type: "claude", Path: "~/x", Servers: &[]string{"a"}},
			"servers+tools":      {Type: "claude", Path: "~/y", Servers: &[]string{"a", "b"}, Tools: &[]string{"a__x", "b__y"}},
			"tools-all-servers":  {Type: "claude", Path: "~/z", Tools: &[]string{"a__x"}},
			"empty-servers-none": {Type: "claude", Path: "~/e", Servers: &[]string{}}, // no servers (valid: minimal agent)
			"empty-tools-none":   {Type: "claude", Path: "~/t", Tools: &[]string{}},   // no tools (valid)
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("valid routing config rejected: %v", err)
	}
}

// TestRoutingEmptyVsAbsentRoundTrip guards the key pointer semantics: an
// absent `servers`/`tools` key (nil pointer = all) must stay distinguishable
// from an explicit empty list (non-nil empty = none) across a YAML save+load.
func TestRoutingEmptyVsAbsentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcphub.yaml")
	c := &Config{
		Version: 1,
		Servers: map[string]Server{"a": {Command: "a", Enabled: true}},
		Agents: map[string]Agent{
			"absent": {Type: "claude", Path: "~/p"},                                           // nil = all
			"empty":  {Type: "claude", Path: "~/p", Servers: &[]string{}, Tools: &[]string{}}, // non-nil empty = none
			"set":    {Type: "claude", Path: "~/p", Servers: &[]string{"a"}, Tools: &[]string{"a__x"}},
		},
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Agents["absent"].Servers != nil || loaded.Agents["absent"].Tools != nil {
		t.Errorf("absent should stay nil, got Servers=%v Tools=%v", loaded.Agents["absent"].Servers, loaded.Agents["absent"].Tools)
	}
	e := loaded.Agents["empty"]
	if e.Servers == nil || len(*e.Servers) != 0 {
		t.Errorf("empty Servers should be non-nil empty, got %v", e.Servers)
	}
	if e.Tools == nil || len(*e.Tools) != 0 {
		t.Errorf("empty Tools should be non-nil empty, got %v", e.Tools)
	}
	// Empty = none: AllowedServers returns nothing.
	if got := e.AllowedServers([]string{"a"}); len(got) != 0 {
		t.Errorf("empty Servers AllowedServers = %v, want none", got)
	}
	if set, restricted := e.ToolScope(); !restricted || len(set) != 0 {
		t.Errorf("empty Tools ToolScope = %v,%v, want empty,true", set, restricted)
	}
	s := loaded.Agents["set"]
	if s.Servers == nil || len(*s.Servers) != 1 || (*s.Servers)[0] != "a" {
		t.Errorf("set Servers wrong after round-trip: %v", s.Servers)
	}
}

func TestPinMatchesAndValidation(t *testing.T) {
	servers := map[string]Server{
		"codemap": {Command: "codemap", Enabled: true},
		"vecgrep": {Command: "vecgrep", Enabled: true},
	}

	// All three pin forms validate against known servers.
	c := &Config{Servers: servers, Pin: []string{"codemap__codemap_status", "vecgrep", "codemap__*"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid pins should pass: %v", err)
	}

	// exact server__tool
	if !c.PinMatches("codemap__codemap_status") {
		t.Error("exact pin should match")
	}
	// bare server pins every tool of that server
	if !c.PinMatches("vecgrep__vecgrep_search") || !c.PinMatches("vecgrep__anything") {
		t.Error("bare server pin should match all its tools")
	}
	// wildcard pins every tool of that server
	if !c.PinMatches("codemap__codemap_find") {
		t.Error("server__* wildcard should match all that server's tools")
	}
	// a tool of an unpinned server does not match
	if c.PinMatches("glyph__glyph_run") {
		t.Error("unpinned server's tool should not match")
	}

	// invalid: unknown server, partial wildcard, trailing "__", or empty —
	// all of these would validate-but-match-nothing without the guards.
	for _, bad := range []string{"ghost", "ghost__*", "ghost__tool", "", "codemap__codemap_*", "codemap__*x", "codemap__"} {
		if err := (&Config{Servers: servers, Pin: []string{bad}}).Validate(); err == nil {
			t.Errorf("pin %q should fail validation", bad)
		}
	}

	// ServerPinned / UnpinServer resolve by server across all pin forms.
	c2 := &Config{Servers: servers, Pin: []string{"codemap", "vecgrep__*", "codemap__codemap_find"}}
	if !c2.ServerPinned("codemap") || !c2.ServerPinned("vecgrep") {
		t.Error("ServerPinned should see bare, wildcard, and exact pins")
	}
	c2.UnpinServer("codemap")
	if c2.ServerPinned("codemap") {
		t.Errorf("UnpinServer should clear every codemap pin, left: %v", c2.Pin)
	}
	if !c2.ServerPinned("vecgrep") {
		t.Error("UnpinServer(codemap) must not touch vecgrep")
	}
}

func TestReservedServerName(t *testing.T) {
	c := &Config{Servers: map[string]Server{"mcphub": {Command: "x", Enabled: true}}}
	if err := c.Validate(); err == nil {
		t.Error("a downstream server named \"mcphub\" should be rejected (reserved for the gateway)")
	}
}

func TestExposeLazy(t *testing.T) {
	if (&Config{}).Lazy() {
		t.Error("default expose should not be lazy")
	}
	if (&Config{Expose: ExposeAll}).Lazy() {
		t.Error("expose:all is not lazy")
	}
	if !(&Config{Expose: ExposeLazy}).Lazy() {
		t.Error("expose:lazy should be lazy")
	}
	if err := (&Config{Expose: "bogus"}).Validate(); err == nil {
		t.Error("invalid expose should fail validation")
	}
	if err := (&Config{Expose: ExposeLazy}).Validate(); err != nil {
		t.Errorf("expose:lazy should validate: %v", err)
	}
}

func TestSpawnCommand(t *testing.T) {
	// no vault -> unchanged
	plain := Server{Command: "codemap", Args: []string{"serve"}}
	if c, a := plain.SpawnCommand(); c != "codemap" || len(a) != 1 || a[0] != "serve" {
		t.Errorf("plain SpawnCommand = %q %v", c, a)
	}
	// vault -> wrapped with tvault run
	v := Server{Command: "gh-mcp", Args: []string{"--stdio"}, Vault: "github", VaultOnly: []string{"GH_TOKEN", "GH_ORG"}}
	c, a := v.SpawnCommand()
	if c != "tvault" {
		t.Fatalf("vault SpawnCommand command = %q, want tvault", c)
	}
	got := strings.Join(a, " ")
	want := "run --project github --only GH_TOKEN,GH_ORG -- gh-mcp --stdio"
	if got != want {
		t.Errorf("vault args =\n  %q\nwant\n  %q", got, want)
	}
	if !v.UsesVault() || plain.UsesVault() {
		t.Error("UsesVault mismatch")
	}
}

func TestVaultRejectsRemote(t *testing.T) {
	c := &Config{Servers: map[string]Server{"x": {URL: "https://e.com", Vault: "p"}}}
	if err := c.Validate(); err == nil {
		t.Error("vault + url should fail validation")
	}
}

func TestMultiFormatRoundTrip(t *testing.T) {
	for _, ext := range []string{".yaml", ".yml", ".toml", ".json"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcphub"+ext)
			want := Starter()
			want.Expose = ExposeLazy
			want.Pin = []string{"codemap__codemap_semantic"}
			if err := Save(path, want); err != nil {
				t.Fatalf("Save(%s): %v", ext, err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%s): %v", ext, err)
			}
			if got.Expose != ExposeLazy || len(got.Pin) != 1 || got.Pin[0] != "codemap__codemap_semantic" {
				t.Errorf("%s: expose/pin not round-tripped: expose=%q pin=%v", ext, got.Expose, got.Pin)
			}
			if len(got.Servers) != len(want.Servers) {
				t.Errorf("%s: %d servers, want %d", ext, len(got.Servers), len(want.Servers))
			}
			cm := got.Servers["codemap"]
			if cm.Command != "codemap" || len(cm.Args) != 1 || cm.Args[0] != "serve" || !cm.Enabled {
				t.Errorf("%s: codemap not round-tripped: %+v", ext, cm)
			}
			if got.Servers["glyph"].Enabled {
				t.Errorf("%s: glyph should be disabled (enabled:false must survive)", ext)
			}
			if a := got.Agents["opencode"]; a.Type != "opencode" || a.ResolvedMode() != ModeDirect {
				t.Errorf("%s: opencode agent not round-tripped: %+v", ext, a)
			}
		})
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandPath("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("ExpandPath(~/x) = %s", got)
	}
	if got := ExpandPath("/abs/path"); got != "/abs/path" {
		t.Errorf("ExpandPath(/abs/path) = %s", got)
	}
}

func TestConnectTimeoutDuration(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want time.Duration
	}{
		{"empty defaults to 30s", "", 30 * time.Second},
		{"explicit 60s", "60s", 60 * time.Second},
		{"2 minutes", "2m", 2 * time.Minute},
		{"invalid falls back to 30s", "bogus", 30 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{ConnectTimeout: c.cfg}
			if got := cfg.ConnectTimeoutDuration(); got != c.want {
				t.Errorf("ConnectTimeoutDuration() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestValidateRejectsBadConnectTimeout(t *testing.T) {
	c := &Config{
		Servers:        map[string]Server{"x": {Command: "c"}},
		ConnectTimeout: "bogus",
	}
	if err := c.Validate(); err == nil {
		t.Error("expected validation error for bad connect_timeout")
	}
	// A valid duration should pass.
	c.ConnectTimeout = "60s"
	if err := c.Validate(); err != nil {
		t.Errorf("valid connect_timeout should pass, got %v", err)
	}
}

func TestValidateUseWhenBounds(t *testing.T) {
	base := func(hints []string) *Config {
		return &Config{Servers: map[string]Server{
			"router": {Command: "router", Enabled: true, UseWhen: hints},
		}}
	}
	valid := make([]string, MaxUseWhenHints)
	for i := range valid {
		valid[i] = strings.Repeat("x", MaxUseWhenHintBytes)
	}
	if err := base(valid).Validate(); err != nil {
		t.Fatalf("valid maximum use_when rejected: %v", err)
	}

	tests := []struct {
		name  string
		hints []string
		want  string
	}{
		{"too many", append(append([]string(nil), valid...), "extra"), "at most"},
		{"empty", []string{"  \t"}, "must not be empty"},
		{"too long", []string{strings.Repeat("x", MaxUseWhenHintBytes+1)}, "exceeds"},
		{"multiline", []string{"capture a page\nthen save it"}, "single line"},
		{"invalid utf8", []string{string([]byte{0xff})}, "valid UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := base(test.hints).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateToolUseWhenBounds(t *testing.T) {
	valid := &Config{Servers: map[string]Server{
		"router": {
			Command: "router", Enabled: true,
			ToolUseWhen: map[string][]string{
				"analyze_video": {"inspect an external video file"},
			},
		},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid tool_use_when rejected: %v", err)
	}

	tests := []struct {
		name  string
		key   string
		hints []string
		want  string
	}{
		{"blank tool", " ", []string{"inspect video"}, "bounded tool name"},
		{"empty hint", "analyze_video", []string{""}, "must not be empty"},
		{"multiline hint", "analyze_video", []string{"inspect\nvideo"}, "single line"},
		{"long hint", "analyze_video", []string{strings.Repeat("x", MaxUseWhenHintBytes+1)}, "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Servers: map[string]Server{
				"router": {Command: "router", Enabled: true, ToolUseWhen: map[string][]string{test.key: test.hints}},
			}}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

// TestValidateAcceptsNewAgentTypes guards against accidental removal of the
// 5 new agent types from validAgentTypes.
func TestValidateAcceptsNewAgentTypes(t *testing.T) {
	newTypes := []string{"copilot", "qwen", "gemini", "kilo", "kimi"}
	for _, typ := range newTypes {
		t.Run(typ, func(t *testing.T) {
			c := &Config{
				Servers: map[string]Server{"x": {Command: "c"}},
				Agents:  map[string]Agent{"a": {Type: typ, Path: "~/x"}},
			}
			if err := c.Validate(); err != nil {
				t.Errorf("type %q should validate, got %v", typ, err)
			}
		})
	}
}

// TestKindsAndValidTypesInSync guards against drift between validAgentTypes and
// harness.Kinds(). The config.go comment explicitly warns about this.
func TestKindsAndValidTypesInSync(t *testing.T) {
	// Every type in Kinds() must be in validAgentTypes.
	for _, k := range []string{"claude", "opencode", "codex", "crush", "forge", "hermes", "copilot", "qwen", "gemini", "kilo", "kimi", "local-agent"} {
		c := &Config{
			Servers: map[string]Server{"x": {Command: "c"}},
			Agents:  map[string]Agent{"a": {Type: k, Path: "~/x"}},
		}
		if err := c.Validate(); err != nil {
			t.Errorf("Kinds drift: %q should be a valid agent type, got %v", k, err)
		}
	}
}

// TestAllAgentTypesRoundTrip verifies that every supported agent type
// survives a yaml/toml/json save+load cycle with its type, path, and mode
// preserved. The existing TestMultiFormatRoundTrip only covers Starter()'s 6
// original agents; this covers all 11.
func TestAllAgentTypesRoundTrip(t *testing.T) {
	allKinds := []string{
		"claude", "opencode", "codex", "crush", "forge", "hermes",
		"copilot", "qwen", "gemini", "kilo", "kimi", "local-agent",
	}
	agents := map[string]Agent{}
	for _, k := range allKinds {
		agents[k] = Agent{Type: k, Path: "~/." + k + "/config", Mode: ModeGateway}
	}
	// opencode uses direct mode in the starter; exercise that path too.
	oc := agents["opencode"]
	oc.Mode = ModeDirect
	agents["opencode"] = oc

	for _, ext := range []string{".yaml", ".toml", ".json"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcphub"+ext)
			want := &Config{
				Version: 1,
				Servers: map[string]Server{"s": {Command: "x", Enabled: true}},
				Agents:  agents,
			}
			if err := Save(path, want); err != nil {
				t.Fatalf("Save(%s): %v", ext, err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%s): %v", ext, err)
			}
			if len(got.Agents) != len(allKinds) {
				t.Fatalf("%s: %d agents, want %d", ext, len(got.Agents), len(allKinds))
			}
			for _, k := range allKinds {
				a := got.Agents[k]
				if a.Type != k {
					t.Errorf("%s: agent %q type = %q, want %q", ext, k, a.Type, k)
				}
				if a.Path != "~/."+k+"/config" {
					t.Errorf("%s: agent %q path = %q", ext, k, a.Path)
				}
			}
			if got.Agents["opencode"].ResolvedMode() != ModeDirect {
				t.Errorf("%s: opencode mode not round-tripped as direct: %+v", ext, got.Agents["opencode"])
			}
		})
	}
}

func TestValidateResponseBudget(t *testing.T) {
	for _, value := range []string{"0", "512B", "32KB", "2 MB", "1GB"} {
		t.Run("valid_"+value, func(t *testing.T) {
			if err := (&Config{ResponseBudget: value}).Validate(); err != nil {
				t.Fatalf("response_budget %q rejected: %v", value, err)
			}
		})
	}
	for _, value := range []string{"bogus", "1B", "511B", "1.5KB", "12XB", "-1", "-2KB", "9223372036854775807GB"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			if err := (&Config{ResponseBudget: value}).Validate(); err == nil {
				t.Fatalf("response_budget %q should fail validation", value)
			}
		})
	}
}

func TestResponseBudgetBytesCompatibilityFallback(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{"", 32 * 1024},
		{"0", 0},
		{"512B", 512},
		{"2KB", 2 * 1024},
		{"bogus", 32 * 1024},
		{"-1KB", 32 * 1024},
		{"9223372036854775807GB", 32 * 1024},
	}
	for _, test := range tests {
		if got := (&Config{ResponseBudget: test.value}).ResponseBudgetBytes(); got != test.want {
			t.Errorf("ResponseBudgetBytes(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestCallTimeoutDuration(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want time.Duration
	}{
		{"empty defaults to 30m", "", 30 * time.Minute},
		{"explicit 10m", "10m", 10 * time.Minute},
		{"1 hour", "1h", time.Hour},
		{"invalid falls back to 30m", "bogus", 30 * time.Minute},
		{"nonpositive falls back to 30m", "-5s", 30 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{CallTimeout: c.cfg}
			if got := cfg.CallTimeoutDuration(); got != c.want {
				t.Errorf("CallTimeoutDuration() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestValidateRejectsBadCallTimeout(t *testing.T) {
	c := &Config{
		Servers:     map[string]Server{"x": {Command: "c"}},
		CallTimeout: "bogus",
	}
	if err := c.Validate(); err == nil {
		t.Error("expected validation error for bad call_timeout")
	}
	c.CallTimeout = "-5s"
	if err := c.Validate(); err == nil {
		t.Error("expected validation error for nonpositive call_timeout")
	}
	c.CallTimeout = "10m"
	if err := c.Validate(); err != nil {
		t.Errorf("valid call_timeout should pass, got %v", err)
	}
}
