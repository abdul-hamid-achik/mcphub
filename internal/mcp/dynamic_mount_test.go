package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
)

func TestDynamicMountAddsServerConnectedAfterInitialFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expose string
		pins   []string
	}{
		{name: "expose all", expose: config.ExposeAll},
		{name: "lazy pinned", expose: config.ExposeLazy, pins: []string{"late__hello"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downstream := dynamicDownstreamServer()
			var available atomic.Bool
			endpoint := flakyMCPServer(t, downstream, &available)
			cfg := &config.Config{
				Expose:         tc.expose,
				Pin:            tc.pins,
				ConnectTimeout: "1s",
				Servers: map[string]config.Server{
					"late": {URL: endpoint, Transport: "http", Enabled: true},
				},
			}
			h := hub.New(cfg, nil, nil)
			t.Cleanup(func() { _ = h.Close() })
			s, cancel := dynamicMountServer(t, cfg, h, nil)
			defer cancel()
			client := connectServerClient(t, s.srv)

			assertToolSurface(t, client, func(names map[string]int) bool {
				return len(names) == managementToolCount && names["late__hello"] == 0
			})

			available.Store(true)
			if !h.ReconnectOne(context.Background(), "late") {
				t.Fatal("late downstream did not reconnect")
			}
			assertToolSurface(t, client, func(names map[string]int) bool {
				return len(names) == managementToolCount+1 && names["late__hello"] == 1
			})

			result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "late__hello"})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || textContent(result) != "hello" {
				t.Fatalf("late-mounted tool result = error:%t text:%q", result.IsError, textContent(result))
			}
			advertised, _ := s.mountDiagnostics()
			if advertised != 1 {
				t.Fatalf("advertised downstream tools = %d, want 1", advertised)
			}
		})
	}
}

func TestDynamicMountZeroBudgetStaysMetaOnlyAndRefreshesReport(t *testing.T) {
	downstream := dynamicDownstreamServer()
	var available atomic.Bool
	endpoint := flakyMCPServer(t, downstream, &available)
	cfg := &config.Config{
		Expose:         config.ExposeAll,
		ConnectTimeout: "1s",
		Servers: map[string]config.Server{
			"late": {URL: endpoint, Transport: "http", Enabled: true},
		},
	}
	h := hub.New(cfg, nil, nil)
	t.Cleanup(func() { _ = h.Close() })
	zero := 0
	s, cancel := dynamicMountServer(t, cfg, h, &agentScope{toolSchemaBudget: &zero})
	defer cancel()
	client := connectServerClient(t, s.srv)

	available.Store(true)
	if !h.ReconnectOne(context.Background(), "late") {
		t.Fatal("late downstream did not reconnect")
	}
	eventually(t, func() bool {
		advertised, report := s.mountDiagnostics()
		return advertised == 0 &&
			report != nil &&
			report.EligibleTools == 1 &&
			report.AdvertisedTools == 0 &&
			report.OmittedTools == 1
	})
	assertToolSurface(t, client, func(names map[string]int) bool {
		return len(names) == managementToolCount && names["late__hello"] == 0
	})
}

func TestDynamicMountRespectsToolScopeAfterReconnect(t *testing.T) {
	downstream := dynamicDownstreamServer()
	var available atomic.Bool
	endpoint := flakyMCPServer(t, downstream, &available)
	cfg := &config.Config{
		Expose:         config.ExposeAll,
		ConnectTimeout: "1s",
		Servers: map[string]config.Server{
			"late": {URL: endpoint, Transport: "http", Enabled: true},
		},
	}
	h := hub.New(cfg, nil, nil)
	t.Cleanup(func() { _ = h.Close() })
	s, cancel := dynamicMountServer(t, cfg, h, &agentScope{
		tools: map[string]bool{"late__goodbye": true},
	})
	defer cancel()
	client := connectServerClient(t, s.srv)

	available.Store(true)
	if !h.ReconnectOne(context.Background(), "late") {
		t.Fatal("late downstream did not reconnect")
	}
	downstream.AddTool(
		&sdk.Tool{Name: "goodbye", Description: "allowed", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "goodbye"}}}, nil
		},
	)
	assertToolSurface(t, client, func(names map[string]int) bool {
		return len(names) == managementToolCount+1 &&
			names["late__hello"] == 0 &&
			names["late__goodbye"] == 1
	})
	advertised, report := s.mountDiagnostics()
	if advertised != 1 || report != nil {
		t.Fatalf("mount diagnostics = advertised:%d report:%+v, want 1 and nil", advertised, report)
	}
}

func TestDynamicMountTracksDownstreamToolListChanges(t *testing.T) {
	downstream := dynamicDownstreamServer()
	var available atomic.Bool
	available.Store(true)
	endpoint := flakyMCPServer(t, downstream, &available)
	cfg := &config.Config{
		Expose:         config.ExposeAll,
		ConnectTimeout: "1s",
		Servers: map[string]config.Server{
			"live": {URL: endpoint, Transport: "http", Enabled: true},
		},
	}
	h := hub.New(cfg, nil, nil)
	t.Cleanup(func() { _ = h.Close() })
	s, cancel := dynamicMountServer(t, cfg, h, nil)
	defer cancel()

	var notifications atomic.Int32
	client := connectServerClientWithToolChanges(t, s.srv, &notifications)
	assertToolSurface(t, client, func(names map[string]int) bool {
		return len(names) == managementToolCount+1 && names["live__hello"] == 1
	})

	downstream.AddTool(
		&sdk.Tool{Name: "goodbye", Description: "new", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "goodbye"}}}, nil
		},
	)
	assertToolSurface(t, client, func(names map[string]int) bool {
		return len(names) == managementToolCount+2 &&
			names["live__hello"] == 1 &&
			names["live__goodbye"] == 1
	})
	eventually(t, func() bool { return notifications.Load() > 0 })

	downstream.RemoveTools("hello")
	assertToolSurface(t, client, func(names map[string]int) bool {
		return len(names) == managementToolCount+1 &&
			names["live__hello"] == 0 &&
			names["live__goodbye"] == 1
	})
	advertised, _ := s.mountDiagnostics()
	if advertised != 1 {
		t.Fatalf("advertised downstream tools after removal = %d, want 1", advertised)
	}
}

func dynamicDownstreamServer() *sdk.Server {
	downstream := sdk.NewServer(&sdk.Implementation{Name: "dynamic", Version: "1"}, nil)
	downstream.AddTool(
		&sdk.Tool{Name: "hello", Description: "initial", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "hello"}}}, nil
		},
	)
	return downstream
}

func flakyMCPServer(t *testing.T, downstream *sdk.Server, available *atomic.Bool) string {
	t.Helper()
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return downstream }, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func dynamicMountServer(t *testing.T, cfg *config.Config, h *hub.Hub, scope *agentScope) (*Server, context.CancelFunc) {
	t.Helper()
	changes, unsubscribe := h.SubscribeChanges()
	h.ConnectMatching(context.Background(), scope.allowsServer)
	s := NewServer(cfg, h, nil, scope)
	if err := s.mountDownstreamTools(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.watchDownstreamTools(ctx, changes)
	t.Cleanup(unsubscribe)
	return s, cancel
}

func connectServerClientWithToolChanges(t *testing.T, server *sdk.Server, notifications *atomic.Int32) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(
		&sdk.Implementation{Name: "dynamic-test", Version: "1"},
		&sdk.ClientOptions{
			ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) {
				notifications.Add(1)
			},
		},
	)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func assertToolSurface(t *testing.T, client *sdk.ClientSession, accept func(map[string]int) bool) {
	t.Helper()
	eventually(t, func() bool {
		list, err := client.ListTools(context.Background(), nil)
		if err != nil {
			return false
		}
		names := make(map[string]int, len(list.Tools))
		for _, tool := range list.Tools {
			names[tool.Name]++
		}
		return accept(names)
	})
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true before timeout")
}
