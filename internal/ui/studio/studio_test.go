package studio

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
			"codemap": {
				Command: "codemap", Args: []string{"serve"}, Enabled: true, Description: "graph",
				Tags: []string{"code", "search"}, UseWhen: []string{"understand symbols and references"},
			},
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

func TestStudioRendersAdvertisementOnlyAgentPolicy(t *testing.T) {
	m, _ := testModel(t)
	noPins := []string{}
	m.cfg.Agents["claude"] = config.Agent{
		Type:             "claude",
		Path:             "~/.claude.json",
		Mode:             config.ModeGateway,
		Pin:              &noPins,
		ToolSchemaBudget: "8KB",
	}
	m.tab = tabAgents
	body := view(m)
	for _, want := range []string{"gateway: pin=[]", "tool_schema_budget=8KB"} {
		if !strings.Contains(body, want) {
			t.Errorf("agents tab missing %q:\n%s", want, body)
		}
	}
}

func TestStudioShowsDiscoveryStateAndRoutingHints(t *testing.T) {
	m, _ := testModel(t)
	body := view(m)
	for _, want := range []string{
		"on-demand",
		"use when: understand symbols and references",
		"tags: code, search",
		"gateway policy: advertised = in tool list",
		"on-demand = resolve when connected + in scope",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("servers tab should show %q:\n%s", want, body)
		}
	}
}

func TestStudioServerExposureStates(t *testing.T) {
	cases := []struct {
		name   string
		expose string
		pins   []string
		on     bool
		want   string
	}{
		{name: "disabled", expose: config.ExposeLazy, on: false, want: "unavailable"},
		{name: "lazy unpinned", expose: config.ExposeLazy, on: true, want: "on-demand"},
		{name: "lazy exact tool pin", expose: config.ExposeLazy, pins: []string{"codemap__find"}, on: true, want: "mixed"},
		{name: "lazy whole server pin", expose: config.ExposeLazy, pins: []string{"codemap"}, on: true, want: "advertised"},
		{name: "all exposure", expose: config.ExposeAll, on: true, want: "advertised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{cfg: &config.Config{Expose: tc.expose, Pin: tc.pins}}
			got := ansiRE.ReplaceAllString(m.serverExposure("codemap", config.Server{Enabled: tc.on}), "")
			if strings.TrimSpace(got) != tc.want {
				t.Fatalf("serverExposure() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStudioWrapsDiscoveryDetailsAtNarrowWidth(t *testing.T) {
	m, _ := testModel(t)
	m.width = 34
	m.cfg.Servers["codemap"] = config.Server{
		Command: "codemap", Enabled: true,
		UseWhen: []string{
			"investigate symbols and references across an unfamiliar codebase",
			"trace structural impact before changing a public contract",
		},
	}

	details := m.wrapServerText(subtleStyle, "      ", "use when: "+strings.Join(m.cfg.Servers["codemap"].UseWhen, " · "))
	legend := m.wrapServerText(dimStyle, "  ", "gateway policy: advertised = in tool list · on-demand = resolve when connected + in scope")
	for name, block := range map[string]string{"details": details, "legend": legend} {
		for _, line := range strings.Split(block, "\n") {
			if got := lipgloss.Width(line); got > m.width {
				t.Errorf("%s line width = %d, want <= %d: %q", name, got, m.width, ansiRE.ReplaceAllString(line, ""))
			}
		}
	}

	plainDetails := strings.Join(strings.Fields(ansiRE.ReplaceAllString(details, "")), " ")
	for _, hint := range m.cfg.Servers["codemap"].UseWhen {
		if !strings.Contains(plainDetails, hint) {
			t.Errorf("wrapped details dropped hint %q:\n%s", hint, plainDetails)
		}
	}

	rendered := m.renderServers()
	plainRendered := strings.Join(strings.Fields(ansiRE.ReplaceAllString(rendered, "")), " ")
	for _, want := range []string{
		"use when: investigate",
		"+1 more",
		"gateway policy: advertised = in tool list",
		"on-demand = resolve when connected + in scope",
	} {
		if !strings.Contains(plainRendered, want) {
			t.Errorf("narrow servers view dropped %q:\n%s", want, plainRendered)
		}
	}
	if strings.Contains(plainRendered, m.cfg.Servers["codemap"].UseWhen[1]) {
		t.Errorf("selected-row preview should not expand every hint:\n%s", plainRendered)
	}

	inWrappedBlock := false
	for _, line := range strings.Split(rendered, "\n") {
		plain := ansiRE.ReplaceAllString(line, "")
		if strings.Contains(plain, "use when:") || strings.Contains(plain, "gateway policy:") {
			inWrappedBlock = true
		}
		if inWrappedBlock && strings.Contains(plain, "vecgrep") {
			inWrappedBlock = false
			continue
		}
		if inWrappedBlock && lipgloss.Width(line) > m.width {
			t.Errorf("rendered wrapped line width = %d, want <= %d: %q", lipgloss.Width(line), m.width, plain)
		}
	}
}

func TestStudioBoundsMaximumRoutingMetadataHeight(t *testing.T) {
	m, _ := testModel(t)
	m.width = 80
	hints := make([]string, config.MaxUseWhenHints)
	for i := range hints {
		hints[i] = strings.Repeat(fmt.Sprintf("hint-%d ", i), 30)
	}
	srv := m.cfg.Servers["codemap"]
	srv.UseWhen = hints
	srv.Tags = []string{strings.Repeat("very-long-tag ", 40)}
	m.cfg.Servers["codemap"] = srv

	rendered := m.renderServers()
	if lines := strings.Count(rendered, "\n") + 1; lines > 10 {
		t.Fatalf("maximum routing metadata expanded to %d lines:\n%s", lines, ansiRE.ReplaceAllString(rendered, ""))
	}
	plain := ansiRE.ReplaceAllString(rendered, "")
	if !strings.Contains(plain, "+7 more") {
		t.Fatalf("bounded preview did not disclose omitted hints:\n%s", plain)
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
	if !strings.Contains(view(m), "[pin]") || !strings.Contains(view(m), "advertised") {
		t.Error("a pinned server should show the pin indicator")
	}
	m = update(m, "p") // unpin
	cfg, _ = config.Load(cfgPath)
	if len(cfg.Pin) != 0 {
		t.Errorf("second p should unpin, got %v", cfg.Pin)
	}
	if !strings.Contains(m.status, "eligible on demand when connected and in scope") {
		t.Errorf("unpin status should explain lazy discovery, got %q", m.status)
	}
}

func TestStudioPinUpgradesMixedServerBeforeUnpinning(t *testing.T) {
	m, cfgPath := testModel(t)
	m.cfg.Pin = []string{"codemap__find", "codemap__inspect"}
	if err := config.Save(cfgPath, m.cfg); err != nil {
		t.Fatal(err)
	}

	m = update(m, "p")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Pin, []string{"codemap"}) {
		t.Fatalf("p should upgrade mixed pins to the whole server, got %v", cfg.Pin)
	}
	if got := ansiRE.ReplaceAllString(m.serverExposure("codemap", cfg.Servers["codemap"]), ""); strings.TrimSpace(got) != "advertised" {
		t.Fatalf("upgraded exposure = %q", got)
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
