package studio

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// view returns the model's rendered content with ANSI styling stripped, so
// assertions can match plain text even when lipgloss styles per-grapheme.
func view(m Model) string { return ansiRE.ReplaceAllString(m.View().Content, "") }

func testModel(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcphub.yaml")
	cfg := &config.Config{
		Version: 1,
		Expose:  config.ExposeLazy,
		Servers: map[string]config.Server{
			"codemap": {Command: "codemap", Args: []string{"serve"}, Enabled: true, Description: "graph"},
			"vecgrep": {Command: "vecgrep", Enabled: false},
		},
		Agents: map[string]config.Agent{
			"claude": {Type: "claude", Path: "~/.claude.json", Mode: config.ModeGateway},
		},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(cfg, cfgPath, st), cfgPath
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "space":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, Text: " "})
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	default:
		return tea.KeyPressMsg(tea.Key{Code: rune(s[0]), Text: s})
	}
}

func update(m Model, s string) Model {
	next, _ := m.Update(key(s))
	return next.(Model)
}

func TestStudioRendersTabsAndHeader(t *testing.T) {
	m, _ := testModel(t)
	body := view(m)
	for _, want := range []string{"mcphub studio", "Servers", "Agents", "Stats", "expose: lazy", "codemap"} {
		if !strings.Contains(body, want) {
			t.Errorf("view missing %q\n---\n%s", want, body)
		}
	}
}

func TestStudioToggleServerPersists(t *testing.T) {
	m, cfgPath := testModel(t)
	// codemap is first (sorted), enabled; space toggles it off and saves.
	m = update(m, "space")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["codemap"].Enabled {
		t.Error("space should have toggled codemap off and persisted")
	}
	if !strings.Contains(m.status, "codemap") {
		t.Errorf("status should mention codemap, got %q", m.status)
	}
}

func TestStudioTabSwitchToAgents(t *testing.T) {
	m, _ := testModel(t)
	m = update(m, "tab") // Servers -> Agents
	body := view(m)
	if !strings.Contains(body, "claude") || !strings.Contains(body, "gateway") {
		t.Errorf("agents tab should show claude/gateway:\n%s", body)
	}
}

// TestStudioAgentsTabShowsNewTypes verifies the Agents tab renders rows for
// new agent types (kimi, kilo) alongside the existing ones.
func TestStudioAgentsTabShowsNewTypes(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcphub.yaml")
	cfg := &config.Config{
		Version: 1,
		Servers: map[string]config.Server{"s": {Command: "x", Enabled: true}},
		Agents: map[string]config.Agent{
			"claude": {Type: "claude", Path: "~/.claude.json", Mode: config.ModeGateway},
			"kimi":   {Type: "kimi", Path: "~/.kimi/config.toml", Mode: config.ModeGateway},
			"kilo":   {Type: "kilo", Path: "~/.config/kilo/kilo.jsonc", Mode: config.ModeDirect},
		},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(cfg, cfgPath, st)
	m = update(m, "tab") // Servers -> Agents
	body := view(m)
	for _, want := range []string{"claude", "kimi", "kilo", "gateway", "direct"} {
		if !strings.Contains(body, want) {
			t.Errorf("agents tab should show %q:\n%s", want, body)
		}
	}
}

// TestComputeAvailableAgents verifies that agent types whose config files
// exist on disk but aren't in the config are reported as available.
func TestComputeAvailableAgents(t *testing.T) {
	// Build a config with only claude configured.
	cfg := &config.Config{
		Agents: map[string]config.Agent{
			"claude": {Type: "claude", Path: "~/.claude.json"},
		},
	}
	avail := computeAvailableAgents(cfg)
	// The result depends on the real machine — we can't assert specific
	// contents, but we CAN assert that 'claude' is NOT in the list (it's
	// configured) and that the function doesn't panic.
	for _, a := range avail {
		if a == "claude" {
			t.Error("claude is configured, should not be in available list")
		}
	}
}

func TestStudioSyncPanelOpens(t *testing.T) {
	m, _ := testModel(t)
	m = update(m, "s") // open sync preview
	body := view(m)
	if !strings.Contains(body, "Sync preview") {
		t.Errorf("expected sync preview panel:\n%s", body)
	}
	// esc closes it
	m = update(m, "esc")
	if m.syncing {
		t.Error("esc should close the sync panel")
	}
}

func TestStudioToggleExpose(t *testing.T) {
	m, cfgPath := testModel(t) // starts as lazy
	m = update(m, "x")         // -> all
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lazy() {
		t.Error("x should have flipped expose from lazy to all and persisted")
	}
	if !strings.Contains(view(m), "expose: all") {
		t.Errorf("header should reflect expose: all\n%s", view(m))
	}
}

func TestStudioPinToggle(t *testing.T) {
	m, cfgPath := testModel(t) // servers: codemap, vecgrep (codemap first when sorted)
	m = update(m, "p")         // pin codemap
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Pin) != 1 || cfg.Pin[0] != "codemap" {
		t.Fatalf("p should pin codemap, got %v", cfg.Pin)
	}
	if !strings.Contains(view(m), "📌") {
		t.Error("a pinned server should show the pin indicator")
	}
	m = update(m, "p") // unpin
	cfg, _ = config.Load(cfgPath)
	if len(cfg.Pin) != 0 {
		t.Errorf("second p should unpin, got %v", cfg.Pin)
	}
}

func TestStudioQuit(t *testing.T) {
	m, _ := testModel(t)
	next, cmd := m.Update(key("q"))
	if !next.(Model).quitting {
		t.Error("q should set quitting")
	}
	if cmd == nil {
		t.Error("q should return a quit command")
	}
}
