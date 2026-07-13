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
	"os/exec"
	"sort"
	"strings"
	"sync"
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
}

// Connected reports whether the downstream is live.
func (d *Downstream) Connected() bool { return d.session != nil && d.Err == nil }

// Hub aggregates downstream MCP servers.
type Hub struct {
	cfg   *config.Config
	store *store.Store
	log   *log.Logger

	connectTimeout time.Duration

	mu          sync.Mutex
	downstreams []*Downstream
}

// New creates a hub over the given config. store may be nil (telemetry is then
// skipped); log may be nil (a discarding logger is used).
func New(cfg *config.Config, st *store.Store, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.New(os.Stderr)
		logger.SetLevel(log.FatalLevel + 1) // effectively silent
	}
	return &Hub{cfg: cfg, store: st, log: logger, connectTimeout: cfg.ConnectTimeoutDuration()}
}

// Connect spawns and connects to every enabled server concurrently. A server
// that fails to start is recorded with its error and skipped, never aborting
// the whole gateway.
func (h *Hub) Connect(ctx context.Context) {
	names := h.cfg.EnabledServers()
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
	h.downstreams = results
	h.mu.Unlock()

	// Close any sessions a previous Connect opened so a second call (a Studio
	// refresh, a reconnect) can't orphan child processes / SSE connections.
	for _, d := range old {
		if d.session != nil {
			_ = d.session.Close()
		}
	}

	for _, d := range results {
		if d.Err != nil {
			h.log.Warn("downstream unavailable", "server", d.Name, "err", d.Err)
		} else {
			h.log.Info("downstream connected", "server", d.Name, "tools", len(d.Tools))
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
		resolved, err := resolveVaultHeaders(srv.Headers)
		if err != nil {
			d.Err = fmt.Errorf("resolve vault headers: %w", err)
			return d
		}
		srv.Headers = resolved
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "mcphub", Version: version.Version}, nil)
	transport := transportFor(srv)
	session, err := client.Connect(cctx, transport, nil)
	if err != nil {
		d.Err = fmt.Errorf("connect: %w", err)
		return d
	}
	list, err := session.ListTools(cctx, nil)
	if err != nil {
		_ = session.Close()
		d.Err = fmt.Errorf("list tools: %w", err)
		return d
	}
	d.session = session
	d.Tools = list.Tools
	return d
}

func transportFor(srv config.Server) mcp.Transport {
	if srv.IsRemote() {
		httpClient := httpClientFor(srv)
		switch srv.Transport {
		case "sse":
			return &mcp.SSEClientTransport{Endpoint: srv.URL, HTTPClient: httpClient}
		default: // "http" or unset
			return &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: httpClient}
		}
	}
	command, cargs := srv.SpawnCommand()
	cmd := exec.Command(command, cargs...)
	cmd.Env = append(os.Environ(), envPairs(srv.Env)...)
	return &mcp.CommandTransport{Command: cmd}
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
	for k, v := range rt.headers {
		req.Header.Set(k, v)
	}
	return rt.base.RoundTrip(req)
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

func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// Mount registers every aggregated downstream tool onto srv (expose: all).
// Returns the number of tools mounted.
func (h *Hub) Mount(srv *mcp.Server) int {
	return h.mount(srv, func(string) bool { return true })
}

// MountMatching registers only the tools whose namespaced (server__tool) name
// satisfies `pred` — used in lazy mode to keep pinned tools directly callable.
// Returns the number mounted.
func (h *Hub) MountMatching(srv *mcp.Server, pred func(namespaced string) bool) int {
	return h.mount(srv, pred)
}

// mount registers each connected downstream tool that `want` accepts, with a
// namespaced name and a telemetry-recording forwarding handler.
func (h *Hub) mount(srv *mcp.Server, want func(namespaced string) bool) int {
	h.mu.Lock()
	downstreams := h.downstreams
	h.mu.Unlock()

	count := 0
	for _, d := range downstreams {
		if !d.Connected() {
			continue
		}
		for _, tool := range d.Tools {
			namespaced := d.Name + "__" + tool.Name
			if !want(namespaced) {
				continue
			}
			srv.AddTool(namespacedTool(d.Name, tool), h.forward(d, tool.Name, namespaced))
			count++
		}
	}
	return count
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
func (h *Hub) forward(d *Downstream, toolName, namespaced string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args json.RawMessage
		if req.Params != nil {
			args = req.Params.Arguments
		}
		return h.Call(ctx, d.Name, toolName, args)
	}
}

// Call forwards a tool invocation to a downstream server by name, records the
// telemetry, and applies the configured bounded-lossless result policy. It is
// the single code path used by directly mounted, pinned, and lazy meta-tool
// calls.
// ReconnectOne immediately reconnects a single downstream by name. Returns
// true if the reconnection succeeded. Used by Call() on a transport failure
// (SPEC §8.1: immediate reconnect instead of waiting for the background watcher).
func (h *Hub) ReconnectOne(ctx context.Context, server string) bool {
	srv, ok := h.cfg.Servers[server]
	if !ok {
		return false
	}
	nd := h.connectOne(ctx, server, srv)
	if !nd.Connected() {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, d := range h.downstreams {
		if d.Name == server {
			old := d
			h.downstreams[i] = nd
			if old.session != nil {
				_ = old.session.Close()
			}
			return true
		}
	}
	return false
}

func (h *Hub) Call(ctx context.Context, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	d := h.downstream(server)
	if d == nil {
		return nil, fmt.Errorf("unknown server %q", server)
	}
	if !d.Connected() {
		return nil, fmt.Errorf("server %q is not connected", server)
	}
	if _, ok := h.FindTool(server, tool); !ok {
		return nil, fmt.Errorf("tool %q not found on server %q", tool, server)
	}
	namespaced := server + "__" + tool
	start := time.Now()
	res, callErr := d.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if callErr != nil {
		h.record(ctx, server, tool, namespaced, time.Since(start), callErr, len(args), 0, res)
		// Transport/protocol failure — the tool call never reached the
		// downstream, so retrying is safe. Invalidate the stale session and
		// attempt an immediate reconnect (SPEC §8.1: don't wait for the
		// 30s background watcher).
		h.invalidateDownstream(server)
		if h.ReconnectOne(ctx, server) {
			h.log.Info("immediate reconnect succeeded, retrying call", "server", server, "tool", tool)
			retryStart := time.Now()
			res2, retryErr := h.retryCall(ctx, server, tool, args)
			if retryErr != nil {
				h.record(ctx, server, tool, namespaced, time.Since(retryStart), retryErr, len(args), 0, res2)
				return nil, fmt.Errorf("call %s__%s failed after reconnect: %w (reconnect succeeded but retry failed)", server, tool, retryErr)
			}
			return h.finalizeCall(ctx, server, tool, namespaced, time.Since(retryStart), len(args), res2), nil
		}
		return nil, fmt.Errorf("call %s__%s failed: %w (reconnect attempted but failed; the background watcher will retry)", server, tool, callErr)
	}
	return h.finalizeCall(ctx, server, tool, namespaced, time.Since(start), len(args), res), nil
}

// invalidateDownstream marks a downstream as disconnected so the background
// watcher and immediate reconnect logic know to try again.
func (h *Hub) invalidateDownstream(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.downstreams {
		if d.Name == name {
			d.Err = fmt.Errorf("session invalidated after transport failure")
			break
		}
	}
}

// retryCall performs a single CallTool on a freshly reconnected downstream.
func (h *Hub) retryCall(ctx context.Context, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	d := h.downstream(server)
	if d == nil || !d.Connected() {
		return nil, fmt.Errorf("server %q not connected after reconnect", server)
	}
	return d.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
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
	for _, t := range d.Tools {
		if t.Name == tool {
			return t, true
		}
	}
	return nil, false
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

// finalizeCall is the one successful-call exit for initial and reconnect
// attempts. It marshals the complete result once for telemetry and spooling.
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
			n += len(d.Tools)
		}
	}
	return n
}

// Close tears down all downstream sessions.
func (h *Hub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.downstreams {
		if d.session != nil {
			_ = d.session.Close()
		}
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
func (h *Hub) reconnectFailed(ctx context.Context) {
	// Snapshot the names+configs of failed downstreams under the lock.
	type failed struct {
		index int
		name  string
		srv   config.Server
		old   *Downstream
	}
	var toReconnect []failed
	h.mu.Lock()
	for i, d := range h.downstreams {
		if !d.Connected() {
			toReconnect = append(toReconnect, failed{i, d.Name, h.cfg.Servers[d.Name], d})
		}
	}
	h.mu.Unlock()
	if len(toReconnect) == 0 {
		return
	}
	for _, f := range toReconnect {
		nd := h.connectOne(ctx, f.name, f.srv)
		if nd.Connected() {
			h.mu.Lock()
			// Guard against a concurrent Connect/refresh that may have replaced
			// the slice already; only swap if the old pointer still matches.
			if i := indexOf(h.downstreams, f.old); i >= 0 {
				h.downstreams[i] = nd
			}
			h.mu.Unlock()
			if f.old.session != nil {
				_ = f.old.session.Close()
			}
			h.log.Info("downstream reconnected", "server", f.name, "tools", len(nd.Tools))
		} else {
			h.log.Warn("downstream still unavailable", "server", f.name, "err", nd.Err)
		}
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
