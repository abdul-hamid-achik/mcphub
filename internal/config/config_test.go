package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"unknown group ref": {Servers: map[string]Server{}, Groups: map[string][]string{"g": {"missing"}}},
		"agent no path":     {Servers: map[string]Server{}, Agents: map[string]Agent{"a": {Type: "claude"}}},
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

func TestPinValidationAndSet(t *testing.T) {
	servers := map[string]Server{"codemap": {Command: "codemap", Enabled: true}}

	// valid: namespaced tool on a known server
	ok := &Config{Servers: servers, Pin: []string{"codemap__codemap_status"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid pin should pass: %v", err)
	}
	if set := ok.PinSet(); !set["codemap__codemap_status"] || len(set) != 1 {
		t.Errorf("PinSet = %v", set)
	}

	// invalid: not namespaced
	if err := (&Config{Servers: servers, Pin: []string{"codemap_status"}}).Validate(); err == nil {
		t.Error("a non-namespaced pin should fail validation")
	}
	// invalid: unknown server
	if err := (&Config{Servers: servers, Pin: []string{"ghost__tool"}}).Validate(); err == nil {
		t.Error("a pin referencing an unknown server should fail validation")
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
