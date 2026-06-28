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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
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
	return &Hub{cfg: cfg, store: st, log: logger}
}

// connectTimeout bounds how long we wait for a single downstream to come up.
const connectTimeout = 30 * time.Second

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
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "mcphub", Version: "0.1.0"}, nil)
	transport, err := transportFor(srv)
	if err != nil {
		d.Err = err
		return d
	}
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

func transportFor(srv config.Server) (mcp.Transport, error) {
	if srv.IsRemote() {
		switch srv.Transport {
		case "sse":
			return &mcp.SSEClientTransport{Endpoint: srv.URL}, nil
		default: // "http" or unset
			return &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil
		}
	}
	command, cargs := srv.SpawnCommand()
	cmd := exec.Command(command, cargs...)
	cmd.Env = append(os.Environ(), envPairs(srv.Env)...)
	return &mcp.CommandTransport{Command: cmd}, nil
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
			srv.AddTool(&mcp.Tool{
				Name:        namespaced,
				Description: fmt.Sprintf("[%s] %s", d.Name, tool.Description),
				InputSchema: tool.InputSchema,
			}, h.forward(d, tool.Name, namespaced))
			count++
		}
	}
	return count
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
// telemetry, and returns the result verbatim. It is the single code path both
// the directly-mounted tools (expose: all) and the mcphub_call_tool meta-tool
// (expose: lazy) go through.
func (h *Hub) Call(ctx context.Context, server, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	d := h.downstream(server)
	if d == nil {
		return nil, fmt.Errorf("unknown server %q", server)
	}
	if !d.Connected() {
		return nil, fmt.Errorf("server %q is not connected", server)
	}
	namespaced := server + "__" + tool
	start := time.Now()
	res, callErr := d.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	h.record(ctx, server, tool, namespaced, time.Since(start), callErr, len(args), res)
	if callErr != nil {
		return nil, callErr
	}
	return res, nil
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

func (h *Hub) record(ctx context.Context, server, tool, namespaced string, dur time.Duration, callErr error, argsBytes int, res *mcp.CallToolResult) {
	if h.store == nil {
		return
	}
	resultBytes := 0
	if res != nil {
		if b, err := json.Marshal(res); err == nil {
			resultBytes = len(b)
		}
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
