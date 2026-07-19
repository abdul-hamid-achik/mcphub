// Package studio is mcphub's interactive TUI (the `mcphub studio` command),
// built on charm.land/bubbletea/v2 + lipgloss/v2 with charmbracelet/harmonica
// for spring-animated stat bars. Humans use it to register and offload
// downstream servers, see what each agent harness manages, sync everything
// from one place, and inspect local usage intelligence — without hand-editing
// YAML or any agent's config file.
package studio

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/syncer"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	tabActive     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Underline(true)
	tabInactive   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	onStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	offStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	barStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	barTrack      = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	onDemandStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).MarginTop(1)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("212")).Padding(0, 1)
	addStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	removeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

const (
	tabServers = iota
	tabAgents
	tabStats
	tabCount
)

const maxBarWidth = 28

type tickMsg time.Time

// bar is one animated horizontal stat bar.
type bar struct {
	label  string
	value  int64
	target float64 // equilibrium fraction (0..1)
	pos    float64
	vel    float64
}

// Model is the Studio bubbletea model.
type Model struct {
	cfg     *config.Config
	cfgPath string
	store   *store.Store
	self    string

	servers         []string
	availableAgents []string // types with config files on disk but not in config
	agents          []string
	cursor          int
	tab             int

	totals      store.Totals
	serverStats []store.ServerStat
	managed     map[string]int

	spring    harmonica.Spring
	bars      []bar
	animating bool

	syncing     bool
	syncApplied bool
	syncResults []syncer.AgentResult

	width, height int
	status        string
	quitting      bool
}

// New builds a Studio model over the given config and (optional) store.
func New(cfg *config.Config, cfgPath string, st *store.Store) Model {
	self, _ := os.Executable()
	m := Model{
		cfg:     cfg,
		cfgPath: cfgPath,
		store:   st,
		self:    self,
		servers: cfg.ServerNames(),
		agents:  cfg.AgentNames(),
		spring:  harmonica.NewSpring(harmonica.FPS(60), 6.0, 0.7),
		managed: map[string]int{},
	}
	m.reload()
	return m
}

func (m *Model) reload() {
	m.servers = m.cfg.ServerNames()
	m.agents = m.cfg.AgentNames()
	m.availableAgents = computeAvailableAgents(m.cfg)
	if m.store == nil {
		return
	}
	ctx := context.Background()
	if t, err := m.store.Totals(ctx); err == nil {
		m.totals = t
	}
	if s, err := m.store.ServerStats(ctx); err == nil {
		m.serverStats = s
	}
	for _, a := range m.agents {
		if names, err := m.store.ManagedFor(ctx, a); err == nil {
			m.managed[a] = len(names)
		}
	}
	m.buildBars(true)
}

// buildBars (re)computes the stat bars. When replay is true the positions are
// reset to zero so the spring animates them up to their targets.
func (m *Model) buildBars(replay bool) {
	var maxCalls int64 = 1
	for _, s := range m.serverStats {
		if s.Calls > maxCalls {
			maxCalls = s.Calls
		}
	}
	bars := make([]bar, 0, len(m.serverStats))
	for _, s := range m.serverStats {
		b := bar{label: s.Server, value: s.Calls, target: float64(s.Calls) / float64(maxCalls)}
		if !replay {
			b.pos = b.target
		}
		bars = append(bars, b)
	}
	m.bars = bars
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/60, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if !m.animating {
			return m, nil
		}
		settled := true
		for i := range m.bars {
			m.bars[i].pos, m.bars[i].vel = m.spring.Update(m.bars[i].pos, m.bars[i].vel, m.bars[i].target)
			if abs(m.bars[i].pos-m.bars[i].target) > 0.005 || abs(m.bars[i].vel) > 0.005 {
				settled = false
			}
		}
		if settled {
			m.animating = false
			return m, nil
		}
		return m, tickCmd()

	case tea.KeyPressMsg:
		if m.syncing {
			return m.updateSyncPanel(msg)
		}
		return m.updateMain(msg)
	}
	return m, nil
}

func (m Model) updateMain(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "tab", "right", "l":
		return m.switchTab((m.tab + 1) % tabCount)
	case "shift+tab", "left", "h":
		return m.switchTab((m.tab + tabCount - 1) % tabCount)
	case "1":
		return m.switchTab(tabServers)
	case "2":
		return m.switchTab(tabAgents)
	case "3":
		return m.switchTab(tabStats)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < m.rowCount()-1 {
			m.cursor++
		}
		return m, nil
	case "space", "enter":
		if m.tab == tabServers {
			m.toggle()
		}
		return m, nil
	case "p":
		if m.tab == tabServers {
			m.togglePin()
		}
		return m, nil
	case "s":
		return m.openSync()
	case "x":
		m.toggleExpose()
		return m, nil
	case "r":
		m.cfg, _ = reload(m.cfgPath, m.cfg)
		m.cursor = 0
		m.reload()
		m.status = "reloaded"
		return m, nil
	}
	return m, nil
}

func (m Model) switchTab(to int) (tea.Model, tea.Cmd) {
	m.tab = to
	m.cursor = 0
	if to == tabStats && len(m.bars) > 0 {
		m.buildBars(true)
		m.animating = true
		return m, tickCmd()
	}
	return m, nil
}

func (m Model) rowCount() int {
	switch m.tab {
	case tabServers:
		return len(m.servers)
	case tabAgents:
		return len(m.agents)
	default:
		return 0
	}
}

func (m *Model) toggle() {
	if len(m.servers) == 0 {
		return
	}
	name := m.servers[m.cursor]
	srv := m.cfg.Servers[name]
	srv.Enabled = !srv.Enabled
	m.cfg.Servers[name] = srv
	if err := config.Save(m.cfgPath, m.cfg); err != nil {
		m.status = "save failed: " + err.Error()
		return
	}
	state := "disabled"
	if srv.Enabled {
		state = "enabled"
	}
	m.status = fmt.Sprintf("%s %s — press s to sync agents", name, state)
}

// --- sync panel -----------------------------------------------------------

func (m Model) openSync() (tea.Model, tea.Cmd) {
	if m.store == nil {
		m.status = "sync needs the intelligence store"
		return m, nil
	}
	m.syncResults = syncer.Reconcile(context.Background(), m.cfg, m.store, m.self, nil, false)
	m.syncing = true
	m.syncApplied = false
	return m, nil
}

func (m Model) updateSyncPanel(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "esc", "q":
		m.syncing = false
		return m, nil
	case "a", "enter":
		if !m.syncApplied {
			m.syncResults = syncer.Reconcile(context.Background(), m.cfg, m.store, m.self, nil, true)
			m.syncApplied = true
			m.reload()
			m.status = "synced all agents"
		}
		return m, nil
	}
	return m, nil
}

// togglePin pins or unpins the selected server (a bare-server pin, which mounts
// all of its tools directly even in lazy mode) and persists it.
func (m *Model) togglePin() {
	if len(m.servers) == 0 {
		return
	}
	name := m.servers[m.cursor]
	if m.serverFullyPinned(name) {
		// Clear every pin that resolves to this server (bare, wildcard, exact),
		// so the indicator and config stay consistent with CLI pins.
		m.cfg.UnpinServer(name)
		if err := config.Save(m.cfgPath, m.cfg); err != nil {
			m.status = "save failed: " + err.Error()
			return
		}
		if m.cfg.Lazy() {
			m.status = name + " unpinned — eligible on demand when connected and in scope"
		} else {
			m.status = name + " unpinned — expose: all still advertises its tools"
		}
		return
	}
	// Upgrade an exact-tool (mixed) pin set to one whole-server pin. This makes
	// `p` a predictable two-state control: mixed/on-demand → advertised → on-demand.
	m.cfg.UnpinServer(name)
	m.cfg.Pin = append(m.cfg.Pin, name)
	if err := config.Save(m.cfgPath, m.cfg); err != nil {
		m.status = "save failed: " + err.Error()
		return
	}
	if m.cfg.Lazy() {
		m.status = name + " pinned — its tools are now advertised directly"
	} else {
		m.status = name + " pinned — its tools will stay advertised in lazy mode"
	}
}

// serverPinned reports whether a server is pinned in any form.
func (m Model) serverPinned(name string) bool { return m.cfg.ServerPinned(name) }

// toggleExpose flips the gateway exposure between all and lazy and persists it.
func (m *Model) toggleExpose() {
	if m.cfg.Lazy() {
		m.cfg.Expose = config.ExposeAll
	} else {
		m.cfg.Expose = config.ExposeLazy
	}
	if err := config.Save(m.cfgPath, m.cfg); err != nil {
		m.status = "save failed: " + err.Error()
		return
	}
	m.status = "exposure → " + m.cfg.Expose + " (restart `mcphub mcp serve` to apply)"
}

func reload(path string, fallback *config.Config) (*config.Config, error) {
	c, err := config.Load(path)
	if err != nil {
		return fallback, err
	}
	return c, nil
}

// --- view -----------------------------------------------------------------

// View implements tea.Model.
func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n\n")
	b.WriteString(m.renderTabs() + "\n\n")
	if m.syncing {
		b.WriteString(m.renderSyncPanel())
	} else {
		switch m.tab {
		case tabServers:
			b.WriteString(m.renderServers())
		case tabAgents:
			b.WriteString(m.renderAgents())
		case tabStats:
			b.WriteString(m.renderStats())
		}
	}
	if m.status != "" {
		b.WriteString("\n" + statusStyle.Render("• "+m.status) + "\n")
	}
	b.WriteString(footerStyle.Render(m.footer()))
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m Model) header() string {
	on := len(m.cfg.EnabledServers())
	expose := m.cfg.Expose
	if expose == "" {
		expose = config.ExposeAll
	}
	meta := fmt.Sprintf("%d servers · %d on · expose: %s · %d agents", len(m.servers), on, expose, len(m.agents))
	return titleStyle.Render("mcphub studio") + "  " + subtleStyle.Render("one catalog, every agent") + "\n" + subtleStyle.Render(meta)
}

func (m Model) renderTabs() string {
	labels := []string{"Servers", "Agents", "Stats"}
	parts := make([]string, len(labels))
	for i, l := range labels {
		if i == m.tab {
			parts[i] = tabActive.Render(l)
		} else {
			parts[i] = tabInactive.Render(l)
		}
	}
	return "  " + strings.Join(parts, "   ")
}

func (m Model) renderServers() string {
	if len(m.servers) == 0 {
		return dimStyle.Render("  No servers. Add one with `mcphub add` or edit mcphub.yaml.")
	}
	var b strings.Builder
	for i, name := range m.servers {
		srv := m.cfg.Servers[name]
		cursor := "  "
		if i == m.cursor {
			cursor = selectedStyle.Render("▶ ")
		}
		mark := offStyle.Render("○ off")
		if srv.Enabled {
			mark = onStyle.Render("● on ")
		}
		kind := "stdio"
		if srv.IsRemote() {
			kind = "remote"
		}
		pin := "     "
		if m.serverPinned(name) {
			pin = selectedStyle.Render("[pin]")
		}
		route := m.serverExposure(name, srv)
		b.WriteString(fmt.Sprintf("%s%s %s %-16s %s %s %s\n",
			cursor, mark, pin, name,
			dimStyle.Render(fmt.Sprintf("%-6s", kind)),
			route,
			dimStyle.Render(srv.Description)))
		if i == m.cursor {
			if len(srv.UseWhen) > 0 {
				suffix := ""
				if remaining := len(srv.UseWhen) - 1; remaining > 0 {
					suffix = fmt.Sprintf(" · +%d more", remaining)
				}
				tail := "… (see mcphub.yaml)"
				if suffix != "" {
					tail = fmt.Sprintf("… (+%d more)", len(srv.UseWhen)-1)
				}
				b.WriteString(m.previewServerText(subtleStyle, "      ", "use when: "+srv.UseWhen[0]+suffix, 2, tail) + "\n")
			} else {
				b.WriteString(m.wrapServerText(dimStyle, "      ", "use when: no routing hints configured") + "\n")
			}
			if len(srv.Tags) > 0 {
				b.WriteString(m.previewServerText(dimStyle, "      ", "tags: "+strings.Join(srv.Tags, ", "), 1, "…") + "\n")
			}
		}
	}
	if m.cfg.Lazy() {
		b.WriteString("\n" + m.previewServerText(dimStyle, "  ", "gateway policy: advertised = in tool list · on-demand = resolve when connected + in scope", 3, "…"))
	}
	return b.String()
}

// wrapServerText wraps selected-row details and the discovery legend to the
// current terminal width while preserving every configured routing hint.
func (m Model) wrapServerText(style lipgloss.Style, indent, value string) string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	indentWidth := lipgloss.Width(indent)
	if indentWidth >= width {
		indent = ""
		indentWidth = 0
	}
	available := width - indentWidth
	if available < 1 {
		available = 1
	}
	lines := strings.Split(style.Width(available).Render(value), "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

// previewServerText keeps selected-row metadata useful in a short terminal.
// The full routing metadata remains in mcphub.yaml and the MCP list response.
func (m Model) previewServerText(style lipgloss.Style, indent, value string, maxLines int, tail string) string {
	wrapped := m.wrapServerText(style, indent, value)
	lines := strings.Split(wrapped, "\n")
	if maxLines < 1 || len(lines) <= maxLines {
		return wrapped
	}
	lines = lines[:maxLines]
	width := m.width
	if width <= 0 {
		width = 100
	}
	indentWidth := lipgloss.Width(indent)
	if indentWidth >= width {
		indent = ""
		indentWidth = 0
	}
	available := max(1, width-indentWidth)
	tail = ansi.Truncate(tail, available, "")
	last := strings.TrimPrefix(lines[len(lines)-1], indent)
	contentWidth := max(0, available-ansi.StringWidth(tail))
	lines[len(lines)-1] = indent + ansi.Truncate(last, contentWidth, "") + tail
	return strings.Join(lines, "\n")
}

// serverExposure describes configured global exposure, not live connection or
// per-agent scope. Exact pins produce a mixed server: some tools are advertised
// and the rest are eligible for on-demand discovery.
func (m Model) serverExposure(name string, srv config.Server) string {
	if !srv.Enabled {
		return offStyle.Render(fmt.Sprintf("%-11s", "unavailable"))
	}
	if !m.cfg.Lazy() || m.serverFullyPinned(name) {
		return onStyle.Render(fmt.Sprintf("%-11s", "advertised"))
	}
	if m.serverPinned(name) {
		return selectedStyle.Render(fmt.Sprintf("%-11s", "mixed"))
	}
	return onDemandStyle.Render(fmt.Sprintf("%-11s", "on-demand"))
}

func (m Model) serverFullyPinned(name string) bool {
	for _, pin := range m.cfg.Pin {
		if pin == name || pin == name+"__*" {
			return true
		}
	}
	return false
}

func (m Model) renderAgents() string {
	var b strings.Builder
	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("  No agents configured. Add an `agents:` block to mcphub.yaml."))
		b.WriteString("\n")
	} else {
		for i, name := range m.agents {
			a := m.cfg.Agents[name]
			cursor := "  "
			if i == m.cursor {
				cursor = selectedStyle.Render("▶ ")
			}
			managed := m.managed[name]
			b.WriteString(fmt.Sprintf("%s%-10s %s  %s  %s\n",
				cursor, name,
				dimStyle.Render(fmt.Sprintf("%-8s", a.Type)),
				onStyle.Render(string(a.ResolvedMode())),
				dimStyle.Render(fmt.Sprintf("manages %d · %s", managed, a.Path))))
			if a.HasRouting() {
				var policy []string
				if a.Servers != nil {
					policy = append(policy, fmt.Sprintf("servers=%v", *a.Servers))
				}
				if a.Tools != nil {
					policy = append(policy, fmt.Sprintf("tools=%v", *a.Tools))
				}
				if a.Pin != nil {
					policy = append(policy, fmt.Sprintf("pin=%v", *a.Pin))
				}
				if a.ToolSchemaBudget != "" {
					policy = append(policy, "tool_schema_budget="+a.ToolSchemaBudget)
				}
				b.WriteString(subtleStyle.Render("      gateway: "+strings.Join(policy, " ")) + "\n")
			}
		}
	}
	// Show available-but-unconfigured agents.
	if len(m.availableAgents) > 0 {
		b.WriteString("\n" + subtleStyle.Render("  available (config file exists, not in mcphub.yaml):"))
		b.WriteString("\n" + subtleStyle.Render("  "+strings.Join(m.availableAgents, ", ")))
		b.WriteString("\n" + dimStyle.Render("  run 'mcphub init --from-agents' to add them"))
	}
	if len(m.agents) > 0 {
		b.WriteString("\n" + dimStyle.Render("  press s to sync these agents from mcphub.yaml"))
	}
	return b.String()
}

// computeAvailableAgents returns the harness kinds whose default config file
// exists on disk but aren't already wired into the config.
func computeAvailableAgents(cfg *config.Config) []string {
	configured := map[string]bool{}
	for _, name := range cfg.AgentNames() {
		configured[cfg.Agents[name].Type] = true
	}
	var avail []string
	for _, kind := range harness.Kinds() {
		if configured[kind] {
			continue
		}
		p := config.ExpandPath(harness.DefaultPath(kind))
		if _, err := os.Stat(p); err == nil {
			avail = append(avail, kind)
		}
	}
	return avail
}

func (m Model) renderStats() string {
	if m.store == nil {
		return dimStyle.Render("  Telemetry store unavailable.")
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %s calls   %s est. tokens   %s errors\n\n",
		selectedStyle.Render(fmt.Sprintf("%d", m.totals.Calls)),
		selectedStyle.Render(fmt.Sprintf("%d", m.totals.EstTokens)),
		selectedStyle.Render(fmt.Sprintf("%d", m.totals.Errors))))
	if len(m.bars) == 0 {
		b.WriteString(dimStyle.Render("  No tool calls recorded yet. Point an agent at mcphub and use it.\n"))
		return b.String()
	}
	for _, bar := range m.bars {
		filled := int(bar.pos*maxBarWidth + 0.5)
		if filled < 0 {
			filled = 0
		}
		if filled > maxBarWidth {
			filled = maxBarWidth
		}
		track := barStyle.Render(strings.Repeat("█", filled)) + barTrack.Render(strings.Repeat("░", maxBarWidth-filled))
		b.WriteString(fmt.Sprintf("  %-16s %s %s\n", bar.label, track, dimStyle.Render(fmt.Sprintf("%d", bar.value))))
	}
	return b.String()
}

func (m Model) renderSyncPanel() string {
	var b strings.Builder
	title := "Sync preview (dry run)"
	if m.syncApplied {
		title = "Sync applied"
	}
	b.WriteString(titleStyle.Render(title) + "\n\n")
	for _, r := range m.syncResults {
		if r.Err != nil {
			b.WriteString(fmt.Sprintf("%-10s %s\n", r.Agent, removeStyle.Render("error: "+r.Err.Error())))
			continue
		}
		if r.Skipped {
			b.WriteString(fmt.Sprintf("%-10s %s\n", r.Agent, dimStyle.Render("disabled, skipped")))
			continue
		}
		head := fmt.Sprintf("%-10s %s", r.Agent, dimStyle.Render(fmt.Sprintf("(%s, %s)", r.Type, r.Mode)))
		if !r.Plan.HasChanges() {
			b.WriteString(head + "  " + dimStyle.Render("up to date") + "\n")
			continue
		}
		b.WriteString(head + "\n")
		for _, ch := range r.Plan.Changes {
			switch ch.Action {
			case "add", "update":
				b.WriteString("    " + addStyle.Render("+ "+ch.Server) + "\n")
			case "remove":
				b.WriteString("    " + removeStyle.Render("- "+ch.Server) + "\n")
			}
		}
	}
	hint := "a/enter apply · esc cancel"
	if m.syncApplied {
		hint = "esc close"
	}
	return panelStyle.Render(b.String()+subtleStyle.Render(hint)) + "\n"
}

func (m Model) footer() string {
	if m.syncing {
		return "a/enter apply · esc cancel"
	}
	switch m.tab {
	case tabServers:
		return "↑/↓ move · space toggle · p pin · s sync · x expose · tab switch · r reload · q quit"
	case tabAgents:
		return "↑/↓ move · s sync · x expose · tab switch · r reload · q quit"
	default:
		return "s sync · x expose · tab switch · r reload · q quit"
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Run launches the Studio TUI.
func Run(cfg *config.Config, cfgPath string, st *store.Store) error {
	p := tea.NewProgram(New(cfg, cfgPath, st))
	_, err := p.Run()
	return err
}
