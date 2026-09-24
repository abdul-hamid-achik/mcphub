package hub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
		h.requestDownstreamCatalogRefresh(d, session)
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

// A refresh that cannot list resources or prompts (a transient failure, or a
// capability error) must keep the last known catalog instead of wiping it.
func TestCatalogRefreshKeepsCatalogsWhenListingFails(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "flaky", Version: "1"}, nil)
	server.AddTool(
		&mcp.Tool{Name: "t", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	)
	server.AddResource(&mcp.Resource{URI: "file:///a", Name: "a"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{}, nil
		})
	server.AddPrompt(&mcp.Prompt{Name: "p"},
		func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{}, nil
		})
	var failLists atomic.Bool
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if failLists.Load() && (method == "resources/list" || method == "prompts/list") {
				return nil, errors.New("transient failure")
			}
			return next(ctx, method, req)
		}
	})
	session := connectInMemoryClient(t, server)
	h := New(&config.Config{ConnectTimeout: "1s"}, nil, nil)
	t.Cleanup(func() { _ = h.Close() })
	d := &Downstream{Name: "flaky"}
	d.setConnection(session, nil)
	d.setResources(listResources(context.Background(), session))
	d.setPrompts(listPrompts(context.Background(), session))
	if got := len(d.PromptsSnapshot()); got != 1 {
		t.Fatalf("test setup: %d prompts at connect, want 1", got)
	}
	if got := len(d.ResourcesSnapshot()); got != 1 {
		t.Fatalf("test setup: %d resources at connect, want 1", got)
	}
	h.mu.Lock()
	h.downstreams = []*Downstream{d}
	h.mu.Unlock()

	failLists.Store(true)
	h.refreshDownstreamCatalogs(context.Background(), d, session)
	if got := len(d.ResourcesSnapshot()); got != 1 {
		t.Fatalf("resources after a failed refresh = %d, want the last known 1", got)
	}
	if got := len(d.PromptsSnapshot()); got != 1 {
		t.Fatalf("prompts after a failed refresh = %d, want the last known 1", got)
	}
}
