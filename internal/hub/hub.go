// Package hub is mcphub's aggregating proxy: the "gateway" half of the
// product. It connects to every enabled downstream MCP server as a client,
// discovers their tools, and re-exposes those tools on a single MCP server
// under namespaced names (server__tool). Every forwarded call is timed and
// recorded in the local intelligence store so `mcphub stats` can show which
// servers and tools actually earn their context budget.
//
// This is the "MCP Docker Kit without Docker" core: one connection, one tool
// list, N servers behind it.
package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/version"
)

// Downstream is one connected (or failed) backing server.
type Downstream struct {
	Name    string
	session *mcp.ClientSession
	Tools   []*mcp.Tool
	Err     error // non-nil if the server failed to connect

	stateMu       sync.RWMutex // guards session and Err after publication in Hub
	toolsMu       sync.RWMutex
	toolRefreshMu sync.Mutex

	refreshStateMu sync.Mutex
	refreshLive    bool
	refreshRunning bool
	refreshPending bool
}

// Connected reports whether the downstream is live.
func (d *Downstream) Connected() bool {
	session, err := d.connectionSnapshot()
	return session != nil && err == nil
}

// connectionSnapshot returns the current session and connection error together.
// Hub publishes downstream pointers to concurrent MCP handlers, so these two
// fields must be read as one coherent state rather than after Hub.mu is
// released by Downstreams or downstream.
func (d *Downstream) connectionSnapshot() (*mcp.ClientSession, error) {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.session, d.Err
}

func (d *Downstream) setConnection(session *mcp.ClientSession, err error) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.session = session
	d.Err = err
}

func (d *Downstream) setError(err error) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.Err = err
}

// ErrorSnapshot returns the current connection error, if any.
func (d *Downstream) ErrorSnapshot() error {
	_, err := d.connectionSnapshot()
	return err
}

// ToolsSnapshot returns a stable copy of the latest downstream tool catalog.
// Tool-list change notifications may replace the catalog while gateway
// handlers are concurrently searching, mounting, or reporting it.
func (d *Downstream) ToolsSnapshot() []*mcp.Tool {
	d.toolsMu.RLock()
	defer d.toolsMu.RUnlock()
	return append([]*mcp.Tool(nil), d.Tools...)
}

func (d *Downstream) setTools(tools []*mcp.Tool) {
	d.toolsMu.Lock()
	defer d.toolsMu.Unlock()
	d.Tools = append([]*mcp.Tool(nil), tools...)
}

// Hub aggregates downstream MCP servers.
type Hub struct {
	cfg   *config.Config
	store *store.Store
	log   *log.Logger

	connectTimeout time.Duration
	now            func() time.Time // injectable clock for detached-call retention tests

	// shutdownCtx is done once Close begins. Detached calls and other
	// background work derive from it so a SIGTERM bounds them instead of
	// waiting out their full timeouts. closing lets hot paths check for
	// shutdown without a context plumbed through.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	closing        atomic.Bool

	mu          sync.Mutex
	downstreams []*Downstream

	detachedMu sync.Mutex
	detached   map[string]*DetachedCall // detached-call registry (see async.go)

	reconnectMu    sync.Mutex             // guards reconnectLocks
	reconnectLocks map[string]*sync.Mutex // serializes immediate reconnects per server

	changeMu   sync.Mutex
	changeSubs map[chan struct{}]struct{}
}

// New creates a hub over the given config. store may be nil (telemetry is then
// skipped); log may be nil (a discarding logger is used).
func New(cfg *config.Config, st *store.Store, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.New(os.Stderr)
		logger.SetLevel(log.FatalLevel + 1) // effectively silent
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	return &Hub{
		cfg: cfg, store: st, log: logger,
		connectTimeout: cfg.ConnectTimeoutDuration(),
		now:            time.Now,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
}

// SubscribeChanges registers a coalescing notification stream for downstream
// connection or tool-catalog changes. Each subscriber has its own one-element
// buffer, so a slow consumer never blocks reconnects; consumers always
// recompute from the latest Hub snapshot. The returned unsubscribe is safe to
// call more than once.
func (h *Hub) SubscribeChanges() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.changeMu.Lock()
	if h.changeSubs == nil {
		h.changeSubs = make(map[chan struct{}]struct{})
	}
	h.changeSubs[ch] = struct{}{}
	h.changeMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.changeMu.Lock()
			delete(h.changeSubs, ch)
			h.changeMu.Unlock()
		})
	}
}

func (h *Hub) notifyDownstreamChange() {
	h.changeMu.Lock()
	subs := make([]chan struct{}, 0, len(h.changeSubs))
	for ch := range h.changeSubs {
		subs = append(subs, ch)
	}
	h.changeMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Connect spawns and connects to every enabled server concurrently. A server
// that fails to start is recorded with its error and skipped, never aborting
// the whole gateway.
func (h *Hub) Connect(ctx context.Context) {
	h.ConnectMatching(ctx, nil)
}

// ConnectMatching replaces the current downstream set with enabled servers
// accepted by allow. A nil allow function connects every enabled server. This
// lets scoped gateway frontends enforce least activation before commands,
// network connections, or secret resolution occur.
func (h *Hub) ConnectMatching(ctx context.Context, allow func(string) bool) {
	names := h.cfg.EnabledServers()
	if allow != nil {
		filtered := names[:0]
		for _, name := range names {
			if allow(name) {
				filtered = append(filtered, name)
			}
		}
		names = filtered
	}
	results := make([]*Downstream, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			results[i] = h.connectOne(ctx, name, h.cfg.Servers[name])
		}(i, name)
	}
	wg.Wait()

	h.mu.Lock()
	old := h.downstreams
	for _, d := range old {
		d.deactivateToolRefresh()
	}
	h.downstreams = results
	h.mu.Unlock()
	for _, d := range results {
		d.activateToolRefresh(h)
	}
	h.notifyDownstreamChange()

	// Close any sessions a previous Connect opened so a second call (a Studio
	// refresh, a reconnect) can't orphan child processes / SSE connections.
	for _, d := range old {
		if session, _ := d.connectionSnapshot(); session != nil {
			_ = session.Close()
		}
	}

	for _, d := range results {
		if err := d.ErrorSnapshot(); err != nil {
			h.log.Warn("downstream unavailable", "server", d.Name, "err", err)
		} else {
			h.log.Info("downstream connected", "server", d.Name, "tools", len(d.ToolsSnapshot()))
		}
	}
}

func (h *Hub) connectOne(ctx context.Context, name string, srv config.Server) *Downstream {
	d := &Downstream{Name: name}
	cctx, cancel := context.WithTimeout(ctx, h.connectTimeout)
	defer cancel()

	// Resolve tvault:// references in remote-server headers before creating
	// the transport. Fail fast with a clear error if a secret can't be fetched.
	if len(srv.Headers) > 0 {
		resolved, err := resolveVaultHeaders(cctx, srv.Headers)
		if err != nil {
			d.setError(fmt.Errorf("resolve vault headers: %w", err))
			return d
		}
		srv.Headers = resolved
	}

	client := mcp.NewClient(
		&mcp.Implementation{Name: "mcphub", Version: version.Version},
		&mcp.ClientOptions{
			ToolListChangedHandler: func(_ context.Context, req *mcp.ToolListChangedRequest) {
				// Coalesce notifications before starting work. A notification
				// can arrive while connectOne is still listing tools, so the
				// downstream is activated only after it is published in Hub.
				h.requestDownstreamToolRefresh(d, req.Session)
			},
		},
	)
	prepared := prepareTransport(srv)
	session, err := client.Connect(cctx, prepared.transport, nil)
	if err != nil {
		if detail := prepared.startupDetail(); detail != "" {
			d.setError(fmt.Errorf("connect: %w; downstream stderr: %s", err, detail))
		} else {
			d.setError(fmt.Errorf("connect: %w", err))
		}
		return d
	}
	d.toolRefreshMu.Lock()
	list, err := session.ListTools(cctx, nil)
	d.toolRefreshMu.Unlock()
	if err != nil {
		_ = session.Close()
		d.setError(fmt.Errorf("list tools: %w", err))
		return d
	}
	d.setConnection(session, nil)
	d.setTools(list.Tools)
	return d
}

func (d *Downstream) activateToolRefresh(h *Hub) {
	if h.closing.Load() {
		return
	}
	d.refreshStateMu.Lock()
	d.refreshLive = true
	start := d.refreshPending && !d.refreshRunning
	if start {
		d.refreshPending = false
		d.refreshRunning = true
	}
	d.refreshStateMu.Unlock()
	if start {
		session, _ := d.connectionSnapshot()
		go h.runDownstreamToolRefresh(d, session)
	}
}

func (d *Downstream) deactivateToolRefresh() {
	d.refreshStateMu.Lock()
	d.refreshLive = false
	d.refreshPending = false
	d.refreshStateMu.Unlock()
}

func (h *Hub) requestDownstreamToolRefresh(d *Downstream, session *mcp.ClientSession) {
	if session == nil || h.closing.Load() {
		return
	}
	d.refreshStateMu.Lock()
	d.refreshPending = true
	start := d.refreshLive && !d.refreshRunning
	if start {
		d.refreshPending = false
		d.refreshRunning = true
	}
	d.refreshStateMu.Unlock()
	if start {
		go h.runDownstreamToolRefresh(d, session)
	}
}

func (h *Hub) runDownstreamToolRefresh(d *Downstream, session *mcp.ClientSession) {
	for {
		h.refreshDownstreamTools(h.shutdownCtx, d, session)

		d.refreshStateMu.Lock()
		if !d.refreshLive || h.closing.Load() {
			d.refreshRunning = false
			d.refreshPending = false
			d.refreshStateMu.Unlock()
			return
		}
		if d.refreshPending {
			d.refreshPending = false
			d.refreshStateMu.Unlock()
			continue
		}
		d.refreshRunning = false
		d.refreshStateMu.Unlock()
		return
	}
}

func (h *Hub) refreshDownstreamTools(ctx context.Context, d *Downstream, session *mcp.ClientSession) {
	if session == nil || h.closing.Load() {
		return
	}
	d.toolRefreshMu.Lock()
	defer d.toolRefreshMu.Unlock()
	rctx, cancel := context.WithTimeout(ctx, h.connectTimeout)
	defer cancel()
	list, err := session.ListTools(rctx, nil)
	if err != nil {
		h.log.Warn("refresh downstream tools failed", "server", d.Name, "err", err)
		return
	}

	installed := false
	h.mu.Lock()
	for _, current := range h.downstreams {
		currentSession, _ := current.connectionSnapshot()
		if !h.closing.Load() && current == d && currentSession == session && current.Connected() {
			d.setTools(list.Tools)
			installed = true
			break
		}
	}
	h.mu.Unlock()
	if installed {
		h.notifyDownstreamChange()
	}
}

func transportFor(srv config.Server) mcp.Transport {
	return prepareTransport(srv).transport
}

// httpClientFor builds an *http.Client for remote transports. When the server
// defines custom headers, a round-tripper injects them into every request.
// For localhost HTTPS endpoints (common with local MCP servers using
// self-signed certs), TLS verification is skipped.
func httpClientFor(srv config.Server) *http.Client {
	localhostTLS := isLocalhostHTTPS(srv.URL)
	if len(srv.Headers) == 0 && !localhostTLS {
		return nil // use http.DefaultClient
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if localhostTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: tr}
	if len(srv.Headers) > 0 {
		client.Transport = &headerRoundTripper{base: tr, headers: srv.Headers}
	}
	return client
}

// headerRoundTripper wraps an http.RoundTripper and sets custom headers on
// every outgoing request before delegating.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (rt *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone first: http.RoundTripper must not mutate the caller's request.
	// Concurrent remote MCP calls (and redirect handling) can share header maps.
	r := req.Clone(req.Context())
	for k, v := range rt.headers {
		r.Header.Set(k, v)
	}
	return rt.base.RoundTrip(r)
}

// isLocalhostHTTPS reports whether rawURL is an https:// URL pointing at a
// loopback address. Local services commonly use self-signed certificates;
// skipping verification for loopback is safe (no DNS rebinding surface).
func isLocalhostHTTPS(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// Mount registers every aggregated downstream tool onto srv (expose: all).
// Returns the number of tools mounted.
func (h *Hub) Mount(srv *mcp.Server) int {
	return h.MountMatching(srv, func(string) bool { return true })
}

// MountMatching registers only the tools whose namespaced (server__tool) name
// satisfies `pred` — used in lazy mode to keep pinned tools directly callable.
// Returns the number mounted.
func (h *Hub) MountMatching(srv *mcp.Server, pred func(namespaced string) bool) int {
	tools := h.MatchingTools(pred)
	for _, tool := range tools {
		srv.AddTool(tool.Definition, tool.Handler)
	}
	return len(tools)
}

// ToolMount is one complete downstream definition and forwarding handler
// selected for direct advertisement by the gateway.
type ToolMount struct {
	Definition *mcp.Tool
	Handler    mcp.ToolHandler
}

// ToolMountReport describes an opt-in admission pass over directly advertised
// downstream tool definitions. Byte counts cover the complete namespaced
// mcp.Tool JSON values (description, schemas, annotations, icons, _meta, and
// future SDK fields), not just InputSchema. The token figures are explicitly
// approximate at four serialized bytes per token.
//
// AdvertisedNames and OmittedNames list the namespaced tool names from the
// deterministic first-fit pass so operators can answer "why isn't X listed?"
// without re-running size math by hand. Both slices are empty when no budget
// was applied or nothing was eligible.
type ToolMountReport struct {
	BudgetBytes               int      `json:"budget_bytes"`
	EligibleTools             int      `json:"eligible_tools"`
	AdvertisedTools           int      `json:"advertised_tools"`
	OmittedTools              int      `json:"omitted_tools"`
	EligibleDefinitionBytes   int      `json:"eligible_definition_bytes"`
	AdvertisedDefinitionBytes int      `json:"advertised_definition_bytes"`
	EligibleEstimatedTokens   int      `json:"eligible_estimated_tokens"`
	AdvertisedEstimatedTokens int      `json:"advertised_estimated_tokens"`
	AdvertisedNames           []string `json:"advertised_names,omitempty"`
	OmittedNames              []string `json:"omitted_names,omitempty"`
}

type budgetedToolCandidate struct {
	server     string
	tool       string
	namespaced string
	definition *mcp.Tool
	bytes      int
}

// MountMatchingBudgeted deterministically mounts the complete definitions
// accepted by pred while their aggregate serialized size fits budgetBytes.
// Candidates are ordered by namespaced name so connection scheduling and
// downstream tools/list order cannot change the selected surface. A definition
// that does not fit is skipped while later smaller definitions may still use
// the remaining budget. A zero-byte budget mounts no downstream tools.
//
// Metadata preservation remains identical to MountMatching: the selected
// definition is a full copy made by namespacedTool and only its protocol name
// and description prefix differ from the downstream value.
func (h *Hub) MountMatchingBudgeted(srv *mcp.Server, pred func(namespaced string) bool, budgetBytes int) (ToolMountReport, error) {
	tools, report, err := h.MatchingToolsBudgeted(pred, budgetBytes)
	if err != nil {
		return report, err
	}
	for _, tool := range tools {
		srv.AddTool(tool.Definition, tool.Handler)
	}
	return report, nil
}

// MatchingTools returns the deterministic desired direct-advertisement set
// without mutating an MCP server. Callers can diff this plan against a
// previously mounted set before using AddTool/RemoveTools.
func (h *Hub) MatchingTools(pred func(namespaced string) bool) []ToolMount {
	var mounts []ToolMount
	for _, catalog := range h.toolCatalogSnapshot() {
		for _, tool := range catalog.tools {
			if tool == nil {
				continue
			}
			namespaced := catalog.server + "__" + tool.Name
			if !pred(namespaced) {
				continue
			}
			mounts = append(mounts, ToolMount{
				Definition: namespacedTool(catalog.server, tool),
				Handler:    h.forward(catalog.server, tool.Name, namespaced),
			})
		}
	}
	sort.Slice(mounts, func(i, j int) bool {
		return mounts[i].Definition.Name < mounts[j].Definition.Name
	})
	return mounts
}

// MatchingToolsBudgeted returns the deterministic desired mount set and its
// byte/token report without mutating an MCP server.
func (h *Hub) MatchingToolsBudgeted(pred func(namespaced string) bool, budgetBytes int) ([]ToolMount, ToolMountReport, error) {
	report := ToolMountReport{BudgetBytes: budgetBytes}
	if budgetBytes < 0 {
		return nil, report, fmt.Errorf("tool definition budget must not be negative")
	}

	var candidates []budgetedToolCandidate
	for _, catalog := range h.toolCatalogSnapshot() {
		for _, tool := range catalog.tools {
			if tool == nil {
				continue
			}
			namespaced := catalog.server + "__" + tool.Name
			if !pred(namespaced) {
				continue
			}
			definition := namespacedTool(catalog.server, tool)
			encoded, err := json.Marshal(definition)
			if err != nil {
				return nil, report, fmt.Errorf("measure tool definition %s: %w", namespaced, err)
			}
			candidates = append(candidates, budgetedToolCandidate{
				server:     catalog.server,
				tool:       tool.Name,
				namespaced: namespaced,
				definition: definition,
				bytes:      len(encoded),
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].namespaced < candidates[j].namespaced
	})

	report.EligibleTools = len(candidates)
	var mounts []ToolMount
	for _, candidate := range candidates {
		report.EligibleDefinitionBytes += candidate.bytes
		if report.AdvertisedDefinitionBytes+candidate.bytes > budgetBytes {
			report.OmittedNames = append(report.OmittedNames, candidate.namespaced)
			continue
		}
		mounts = append(mounts, ToolMount{
			Definition: candidate.definition,
			Handler:    h.forward(candidate.server, candidate.tool, candidate.namespaced),
		})
		report.AdvertisedTools++
		report.AdvertisedDefinitionBytes += candidate.bytes
		report.AdvertisedNames = append(report.AdvertisedNames, candidate.namespaced)
	}
	report.OmittedTools = report.EligibleTools - report.AdvertisedTools
	report.EligibleEstimatedTokens = estimatedDefinitionTokens(report.EligibleDefinitionBytes)
	report.AdvertisedEstimatedTokens = estimatedDefinitionTokens(report.AdvertisedDefinitionBytes)
	return mounts, report, nil
}

func estimatedDefinitionTokens(serializedBytes int) int {
	if serializedBytes <= 0 {
		return 0
	}
	return (serializedBytes + 3) / 4
}

type downstreamToolCatalog struct {
	server string
	tools  []*mcp.Tool
}

func (h *Hub) toolCatalogSnapshot() []downstreamToolCatalog {
	h.mu.Lock()
	defer h.mu.Unlock()
	catalogs := make([]downstreamToolCatalog, 0, len(h.downstreams))
	for _, d := range h.downstreams {
		if !d.Connected() {
			continue
		}
		catalogs = append(catalogs, downstreamToolCatalog{
			server: d.Name,
			tools:  d.ToolsSnapshot(),
		})
	}
	return catalogs
}

// namespacedTool copies a downstream tool definition, changing only the
// protocol name and description needed by the gateway. Copying the complete
// SDK value preserves titles, input/output schemas, annotations, icons, _meta,
// and fields added by future SDK releases instead of rebuilding a partial tool.
func namespacedTool(server string, tool *mcp.Tool) *mcp.Tool {
	mounted := *tool
	mounted.Name = server + "__" + tool.Name
	mounted.Description = fmt.Sprintf("[%s] %s", server, tool.Description)
	return &mounted
}

// forward returns a raw passthrough handler used when a tool is mounted
// directly (expose: all). It simply relays to Call.
func (h *Hub) forward(server, toolName, namespaced string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args json.RawMessage
		if req.Params != nil {
			args = req.Params.Arguments
		}
		return h.Call(ctx, server, toolName, args)
	}
}

// Call forwards a tool invocation to a downstream server by name, records the
// telemetry, and applies the configured bounded-lossless result policy. It is
// the single code path used by directly mounted, pinned, and lazy meta-tool
// calls.
// ReconnectOne immediately reconnects a single downstream by name. Returns
// true if the server is connected on return — either because this call
// reconnected it or because a concurrent reconnect already had. Call uses this
// after a transport failure to restore future availability, but never repeats
// the uncertain operation.
func (h *Hub) ReconnectOne(ctx context.Context, server string) bool {
	srv, ok := h.cfg.Servers[server]
	if !ok {
		return false
	}
	// Serialize per server: several in-flight calls can observe a transport
	// failure at the same moment (detached calls especially, since their
	// contexts outlive the requesting client). Without this, each failure
	// would spawn its own connect — transiently N sessions/child processes —
	// and every swap would close the session the previous reconnect had just
	// established, spuriously failing calls already routed onto it.
	lock := h.serverReconnectLock(server)
	lock.Lock()
	defer lock.Unlock()
	if h.closing.Load() {
		// A caller that raced past Call's own closing check must not respawn
		// a downstream Close will never tear down.
		return false
	}
	if d := h.downstream(server); d != nil && d.Connected() {
		return true // a concurrent reconnect already restored this server
	}
	nd := h.connectOne(ctx, server, srv)
	if !nd.Connected() {
		return false
	}
	var stale *mcp.ClientSession
	swapped := false
	h.mu.Lock()
	if h.closing.Load() {
		// Close may have started (and even finished its teardown) while we
		// were dialing; installing the fresh session now would leak it. The
		// re-check is decisive under h.mu: if closing is still false here,
		// Close has not begun, so it must later take h.mu and will tear down
		// whatever we swap in.
		h.mu.Unlock()
		if session, _ := nd.connectionSnapshot(); session != nil {
			_ = session.Close()
		}
		return false
	}
	for i, d := range h.downstreams {
		if d.Name == server {
			d.deactivateToolRefresh()
			stale, _ = d.connectionSnapshot()
			h.downstreams[i] = nd
			swapped = true
			break
		}
	}
	h.mu.Unlock()
	if !swapped {
		// The server vanished from the downstream set (a concurrent scoped
		// refresh); close the fresh session instead of leaking it.
		if session, _ := nd.connectionSnapshot(); session != nil {
			_ = session.Close()
		}
		return false
	}
	if stale != nil {
		// Tear the dead session down off the caller's path and outside h.mu:
		// closing a stdio transport can block for its kill grace period while
		// the child is still busy (e.g. mid-call after a gateway-side timeout),
		// and that must stall neither this call's error return nor every other
		// hub operation waiting on the lock.
		go func() { _ = stale.Close() }()
	}
	nd.activateToolRefresh(h)
	h.notifyDownstreamChange()
	return true
}

// serverReconnectLock returns the mutex serializing immediate reconnects for
// one server, creating it on first use. The map is bounded by the set of
// configured server names.
func (h *Hub) serverReconnectLock(server string) *sync.Mutex {
	h.reconnectMu.Lock()
	defer h.reconnectMu.Unlock()
	if h.reconnectLocks == nil {
		h.reconnectLocks = map[string]*sync.Mutex{}
	}
	l, ok := h.reconnectLocks[server]
	if !ok {
		l = &sync.Mutex{}
		h.reconnectLocks[server] = l
	}
	return l
}

func (h *Hub) Call(ctx context.Context, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	d := h.downstream(server)
	if d == nil {
		return nil, fmt.Errorf("unknown server %q", server)
	}
	session, connectionErr := d.connectionSnapshot()
	if session == nil || connectionErr != nil {
		return nil, fmt.Errorf("server %q is not connected", server)
	}
	canonical, _, ok := h.CanonicalTool(server, tool)
	if !ok {
		return nil, fmt.Errorf("tool %q not found on server %q", tool, server)
	}
	tool = canonical
	namespaced := server + "__" + tool
	start := time.Now()
	res, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if callErr != nil {
		h.record(ctx, server, tool, namespaced, time.Since(start), callErr, len(args), 0, res)
		// A transport/protocol failure does not prove the downstream skipped the
		// operation: it may have completed and lost only the response. Reconnect
		// immediately for future calls, but never replay an effect-unknown request
		// without an explicit idempotency contract and caller-supplied key.
		// The reconnect runs on a detached context: when the failure was the
		// request's own timeout/cancellation, ctx is already done and reusing it
		// would guarantee the reconnect fails too (connectOne applies its own
		// bounded connect timeout).
		h.invalidateDownstream(d)
		// During shutdown the failure is expected (Close cancelled the call's
		// context); respawning the downstream now would race the teardown and
		// leave a fresh session/child process nothing will ever close.
		if h.closing.Load() {
			return nil, fmt.Errorf("call %s__%s outcome unknown after transport failure: %w (hub is shutting down; request was not retried and no reconnect was attempted)", server, tool, callErr)
		}
		if h.ReconnectOne(context.WithoutCancel(ctx), server) {
			h.log.Info("immediate reconnect succeeded; uncertain call was not replayed", "server", server, "tool", tool)
			return nil, fmt.Errorf("call %s__%s outcome unknown after transport failure: %w (connection restored for future calls; request was not retried)", server, tool, callErr)
		}
		return nil, fmt.Errorf("call %s__%s outcome unknown after transport failure: %w (reconnect failed; request was not retried and the background watcher will restore the connection)", server, tool, callErr)
	}
	return h.finalizeCall(ctx, server, tool, namespaced, time.Since(start), len(args), res), nil
}

// invalidateDownstream marks the exact downstream entry a failed call was
// using as disconnected, so the background watcher and immediate reconnect
// logic know to try again. Matching by identity (not name) means a failure
// observed on an already-replaced session cannot invalidate the fresh
// connection a concurrent reconnect just installed.
func (h *Hub) invalidateDownstream(target *Downstream) {
	changed := false
	h.mu.Lock()
	for _, d := range h.downstreams {
		if d == target {
			d.setError(fmt.Errorf("session invalidated after transport failure"))
			changed = true
			break
		}
	}
	h.mu.Unlock()
	if changed {
		h.notifyDownstreamChange()
	}
}

// downstream looks up a connected-or-not downstream by name.
func (h *Hub) downstream(name string) *Downstream {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.downstreams {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// FindTool returns the downstream tool definition for (server, tool).
func (h *Hub) FindTool(server, tool string) (*mcp.Tool, bool) {
	d := h.downstream(server)
	if d == nil {
		return nil, false
	}
	for _, t := range d.ToolsSnapshot() {
		if t.Name == tool {
			return t, true
		}
	}
	return nil, false
}

// CanonicalTool resolves tool to its exact downstream name on server,
// accepting the stutter-collapsed alias. Downstream servers commonly
// self-prefix their tool names (hitspec's search tool is hitspec_search_web),
// so the gateway-namespaced form stutters (hitspec__hitspec_search_web) and
// callers reasonably try hitspec__search_web or {server: "hitspec", tool:
// "search_web"} — and previously got a bare "tool not found". The exact name
// always wins; the server-prefixed fallback applies only when the bare name
// matches nothing, so a genuine downstream tool can never be shadowed.
func (h *Hub) CanonicalTool(server, tool string) (string, *mcp.Tool, bool) {
	if t, ok := h.FindTool(server, tool); ok {
		return tool, t, true
	}
	if pref := server + "_" + tool; pref != tool {
		if t, ok := h.FindTool(server, pref); ok {
			return pref, t, true
		}
	}
	return tool, nil, false
}

type resultReceipt struct {
	Status        string `json:"status"`
	CallID        string `json:"callId"`
	Server        string `json:"server,omitempty"`
	Tool          string `json:"tool,omitempty"`
	Namespaced    string `json:"namespaced,omitempty"`
	OriginalBytes int    `json:"originalBytes"`
	BudgetBytes   int    `json:"budgetBytes"`
	Preview       string `json:"preview,omitempty"`
	NextAction    string `json:"nextAction,omitempty"`
}

// finalizeCall is the one successful-call exit. It marshals the complete result
// once for telemetry and spooling.
func (h *Hub) finalizeCall(ctx context.Context, server, tool, namespaced string, dur time.Duration, argsBytes int, res *mcp.CallToolResult) *mcp.CallToolResult {
	payload, err := json.Marshal(res)
	resultBytes := 0
	if err == nil {
		resultBytes = len(payload)
	}
	h.record(ctx, server, tool, namespaced, dur, nil, argsBytes, resultBytes, res)

	budget := h.cfg.ResponseBudgetBytes()
	if err != nil || res == nil || h.store == nil || h.cfg.Verbatim || budget == 0 || resultBytes <= budget {
		return res
	}

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	callID, err := h.store.PutResult(sctx, server, tool, payload)
	if err != nil {
		h.log.Warn("result spool write failed; returning complete result", "server", server, "tool", tool, "err", err)
		return res
	}

	receipt := resultReceipt{
		Status:        "stored",
		CallID:        callID,
		Server:        server,
		Tool:          tool,
		Namespaced:    namespaced,
		OriginalBytes: resultBytes,
		BudgetBytes:   budget,
		NextAction:    "Call mcphub_get_result with this callId and cursor 0, then continue with each nextCursor until done is true.",
	}
	out := receiptResult(receipt, res.IsError)

	// A preview is optional recovery context. If origin metadata makes the
	// base receipt exceed the budget, fall back to the fixed compact receipt;
	// callId is the only field retrieval requires.
	base, marshalErr := json.Marshal(out)
	if marshalErr == nil && len(base) > budget {
		receipt.Server = ""
		receipt.Tool = ""
		receipt.Namespaced = ""
		receipt.NextAction = ""
		out = receiptResult(receipt, res.IsError)
		base, marshalErr = json.Marshal(out)
	}
	if marshalErr == nil {
		for maxPreview := (budget - len(base)) / 6; maxPreview >= utf8.UTFMax; maxPreview /= 2 {
			preview := textPreview(res, maxPreview)
			if preview == "" {
				break
			}
			candidateReceipt := receipt
			candidateReceipt.Preview = preview
			candidate := receiptResult(candidateReceipt, res.IsError)
			encoded, candidateErr := json.Marshal(candidate)
			if candidateErr == nil && len(encoded) <= budget {
				return candidate
			}
		}
	}
	return out
}

func receiptResult(receipt resultReceipt, isError bool) *mcp.CallToolResult {
	text := fmt.Sprintf(
		"Result stored: %d bytes exceeded the %d-byte response budget. Retrieve it with mcphub_get_result using callId %s and cursor 0.",
		receipt.OriginalBytes, receipt.BudgetBytes, receipt.CallID,
	)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: receipt,
		IsError:           isError,
	}
}

func textPreview(res *mcp.CallToolResult, maxBytes int) string {
	for _, content := range res.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok || text.Text == "" {
			continue
		}
		value := strings.ToValidUTF8(text.Text, "\uFFFD")
		if len(value) <= maxBytes {
			return value
		}
		if maxBytes <= len("…") {
			return ""
		}
		end := maxBytes - len("…")
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		if end == 0 {
			return ""
		}
		return value[:end] + "…"
	}
	return ""
}

func (h *Hub) record(ctx context.Context, server, tool, namespaced string, dur time.Duration, callErr error, argsBytes, resultBytes int, res *mcp.CallToolResult) {
	if h.store == nil {
		return
	}
	// A tool that fails its own execution returns (res, nil) with res.IsError —
	// the go-sdk only sets callErr for protocol/transport failures. Count those
	// as errors too, or stats/status would undercount the common failure case.
	recErr := callErr
	if recErr == nil && res != nil && res.IsError {
		recErr = fmt.Errorf("%s", toolErrorText(res))
	}
	// Persist under a detached, time-bounded context: when the agent cancels or
	// the downstream times out, the request ctx is already done, and reusing it
	// would drop exactly the slow/cancelled calls a maintainer most wants to see.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.store.RecordCall(rctx, store.CallRecord{
		Server:      server,
		Tool:        tool,
		Namespaced:  namespaced,
		Duration:    dur,
		Err:         recErr,
		ArgsBytes:   argsBytes,
		ResultBytes: resultBytes,
	}); err != nil {
		h.log.Warn("telemetry write failed", "err", err)
	}
}

// toolErrorText extracts a concise message from an IsError tool result.
func toolErrorText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok && t.Text != "" {
			return t.Text
		}
	}
	return "tool reported error"
}

// Downstreams returns a snapshot of the connection states.
func (h *Hub) Downstreams() []*Downstream {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Downstream, len(h.downstreams))
	copy(out, h.downstreams)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ToolCount returns the total number of mounted tools across live downstreams.
func (h *Hub) ToolCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, d := range h.downstreams {
		if d.Connected() {
			n += len(d.ToolsSnapshot())
		}
	}
	return n
}

// Close tears down all downstream sessions. It first signals shutdown —
// before taking any lock — so in-flight detached calls are cancelled and
// failure paths stop respawning downstreams, bounding how long a SIGTERM
// teardown can take. Session Close runs outside h.mu: a stdio child that
// ignores the kill signal can block for its grace period, and that must not
// convoy every other hub path waiting on the lock.
func (h *Hub) Close() error {
	h.closing.Store(true)
	h.shutdownCancel()
	var sessions []*mcp.ClientSession
	h.mu.Lock()
	for _, d := range h.downstreams {
		d.deactivateToolRefresh()
		if session, _ := d.connectionSnapshot(); session != nil {
			sessions = append(sessions, session)
		}
	}
	h.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
	return nil
}

// watchInterval is how often Watch checks for failed downstreams.
const watchInterval = 30 * time.Second

// Watch periodically reconnects downstreams that failed to connect or whose
// sessions have died. It runs until ctx is cancelled. Call it from a long-lived
// gateway (mcp serve) so a crashed downstream self-heals without restarting the
// agent. It is a no-op for downstreams that are already connected.
func (h *Hub) Watch(ctx context.Context) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.reconnectFailed(ctx)
		}
	}
}

// reconnectFailed tries to reconnect every non-connected downstream. Successful
// reconnects replace the old entry in the slice and close the stale session.
// The fence matches ReconnectOne: skip when the hub is closing, serialize per
// server so Watch cannot thrash with Call-driven reconnect, re-check closing
// under h.mu before install, and tear stale sessions down asynchronously so a
// hung child cannot stall the whole Watch cycle.
func (h *Hub) reconnectFailed(ctx context.Context) {
	if h.closing.Load() {
		return
	}
	// Snapshot the names+configs of failed downstreams under the lock.
	type failed struct {
		name string
		srv  config.Server
		old  *Downstream
	}
	var toReconnect []failed
	h.mu.Lock()
	for _, d := range h.downstreams {
		if !d.Connected() {
			toReconnect = append(toReconnect, failed{d.Name, h.cfg.Servers[d.Name], d})
		}
	}
	h.mu.Unlock()
	if len(toReconnect) == 0 {
		return
	}
	for _, f := range toReconnect {
		if h.closing.Load() {
			return
		}
		lock := h.serverReconnectLock(f.name)
		lock.Lock()
		if h.closing.Load() {
			lock.Unlock()
			return
		}
		if d := h.downstream(f.name); d != nil && d.Connected() {
			// A concurrent ReconnectOne already restored this server.
			lock.Unlock()
			continue
		}
		nd := h.connectOne(ctx, f.name, f.srv)
		if !nd.Connected() {
			lock.Unlock()
			h.log.Warn("downstream still unavailable", "server", f.name, "err", nd.ErrorSnapshot())
			continue
		}
		var stale *mcp.ClientSession
		swapped := false
		h.mu.Lock()
		if h.closing.Load() {
			// Close raced the dial; installing now would leak a session nobody
			// tears down. Discard the fresh connection instead.
			h.mu.Unlock()
			if session, _ := nd.connectionSnapshot(); session != nil {
				_ = session.Close()
			}
			lock.Unlock()
			return
		}
		// Guard against a concurrent Connect/refresh that may have replaced
		// the slice already; only swap if the old pointer still matches.
		if i := indexOf(h.downstreams, f.old); i >= 0 {
			f.old.deactivateToolRefresh()
			stale, _ = f.old.connectionSnapshot()
			h.downstreams[i] = nd
			swapped = true
		}
		h.mu.Unlock()
		if !swapped {
			if session, _ := nd.connectionSnapshot(); session != nil {
				_ = session.Close()
			}
			lock.Unlock()
			continue
		}
		if stale != nil {
			// Same as ReconnectOne: stdio close can block for kill grace.
			go func() { _ = stale.Close() }()
		}
		nd.activateToolRefresh(h)
		h.notifyDownstreamChange()
		h.log.Info("downstream reconnected", "server", f.name, "tools", len(nd.ToolsSnapshot()))
		lock.Unlock()
	}
}

func indexOf(downstreams []*Downstream, d *Downstream) int {
	for i, dd := range downstreams {
		if dd == d {
			return i
		}
	}
	return -1
}
