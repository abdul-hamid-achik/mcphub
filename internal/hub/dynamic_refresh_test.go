package hub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

func TestDownstreamConnectionStateIsSafeDuringInvalidation(t *testing.T) {
	session, tool := inMemoryDownstream(t, &mcp.CallToolResult{})
	d := &Downstream{Name: "memory", session: session, Tools: []*mcp.Tool{tool}}
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{d}
	t.Cleanup(func() { _ = h.Close() })

	const iterations = 1_000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range iterations {
			h.invalidateDownstream(d)
			d.setConnection(session, nil)
		}
	}()
	go func() {
		defer wg.Done()
		for range iterations {
			_, _ = d.connectionSnapshot()
			_ = d.Connected()
			_ = d.ErrorSnapshot()
		}
	}()
	wg.Wait()
}

func TestToolRefreshCoalescesNotificationsBeforePublication(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "dynamic", Version: "1"}, nil)
	server.AddTool(
		&mcp.Tool{Name: "initial", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	)
	session := connectInMemoryClient(t, server)
	d := &Downstream{Name: "dynamic", session: session}
	h := New(&config.Config{ConnectTimeout: "1s"}, nil, nil)
	t.Cleanup(func() { _ = h.Close() })

	server.AddTool(
		&mcp.Tool{Name: "late", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	)
	for range 1_000 {
		h.requestDownstreamToolRefresh(d, session)
	}

	d.refreshStateMu.Lock()
	if !d.refreshPending || d.refreshRunning {
		t.Fatalf("pre-publication refresh state = pending:%t running:%t, want true/false", d.refreshPending, d.refreshRunning)
	}
	d.refreshStateMu.Unlock()

	h.mu.Lock()
	h.downstreams = []*Downstream{d}
	h.mu.Unlock()
	d.activateToolRefresh(h)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.FindTool("dynamic", "late"); ok {
			d.refreshStateMu.Lock()
			settled := !d.refreshPending && !d.refreshRunning
			d.refreshStateMu.Unlock()
			if settled {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("coalesced pre-publication refresh did not publish the latest tool catalog")
}
