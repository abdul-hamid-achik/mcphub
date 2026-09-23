package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	"github.com/abdul-hamid-achik/mcphub/internal/logsink"
	hubmcp "github.com/abdul-hamid-achik/mcphub/internal/mcp"
	"github.com/abdul-hamid-achik/mcphub/internal/version"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp <subcommand>",
		Short: "Run mcphub as an MCP server",
	}
	cmd.AddCommand(newMCPServeCmd())
	return cmd
}

func newMCPServeCmd() *cobra.Command {
	var agentName, listen string
	var stateless bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the gateway MCP server (stdio, or HTTP with --listen)",
		Long: `serve runs mcphub as a single MCP server. It connects to every
enabled downstream server, aggregates their tools under 'server__tool' names,
and records each proxied call to the local intelligence db.

Default is stdio (one agent process per gateway). --listen host:port serves
streamable HTTP instead so many agents can share one process. The same address
can be set as listen: in mcphub.yaml; then mcphub sync writes that URL into
gateway-mode agents and mcphub up starts this listener.

--stateless (or listen_stateless: in mcphub.yaml) makes the HTTP listener serve
the stateless 2026-07-28 MCP protocol. Older agents still connect, but there is
no server-to-agent channel: they lose interactive-question relay and live
list_changed updates.

When --agent <name> is given, the gateway applies that agent's servers/tools
scope plus optional pin and tool_schema_budget.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGateway(agentName, listen, stateless)
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name this gateway serves (applies servers/tools scope and pin/schema advertisement policy)")
	cmd.Flags().StringVar(&listen, "listen", "", "serve streamable HTTP on host:port instead of stdio (overrides config listen:)")
	cmd.Flags().BoolVar(&stateless, "stateless", false, "serve the stateless 2026-07-28 protocol on the HTTP listener (or set listen_stateless: in mcphub.yaml)")
	return cmd
}

func newUpCmd() *cobra.Command {
	var listen string
	var stateless bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Run a shared gateway daemon on streamable HTTP",
		Long: `up starts one mcphub gateway that many agents can share.

It listens on --listen, or listen: in mcphub.yaml, or 127.0.0.1:9820.
Point gateway-mode agents at that URL (mcphub sync does this when listen: is set)
instead of spawning mcphub mcp serve per session.

--stateless (or listen_stateless: in mcphub.yaml) serves the stateless
2026-07-28 MCP protocol (older agents connect but lose interactive-question
relay and live list_changed updates).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGateway("", listen, stateless)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "host:port (default: config listen: or 127.0.0.1:9820)")
	cmd.Flags().BoolVar(&stateless, "stateless", false, "serve the stateless 2026-07-28 protocol (or set listen_stateless: in mcphub.yaml)")
	return cmd
}

func runGateway(agentName, listenFlag string, statelessFlag bool) error {
	c, cfgPath, err := loadConfig()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	scope, err := hubmcp.ScopeFor(c, agentName)
	if err != nil {
		return err
	}

	listen := listenFlag
	if listen == "" {
		listen = c.Listen
	}

	sinkName := "gateway"
	if agentName != "" {
		sinkName = "gateway-" + agentName
	}
	if listen != "" {
		sinkName += "-http"
	}
	var logWriter io.Writer = os.Stderr
	if sink, sinkErr := logsink.New(logsink.DefaultDir(), sinkName); sinkErr == nil {
		defer sink.Close()
		logWriter = io.MultiWriter(os.Stderr, sink)
	}
	logger := log.NewWithOptions(logWriter, log.Options{Prefix: "mcphub", ReportTimestamp: true})
	logger.Info("gateway starting", "version", version.Version, "agent", agentName, "listen", listen)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	h := hub.New(c, st, logger)
	h.SetAgentName(agentName)
	srv := hubmcp.NewServer(c, h, st, scope)
	srv.SetConfigPath(cfgPath)
	srv.SetAgentName(agentName)

	if listen != "" {
		stateless := statelessFlag || c.ListenStateless
		if stateless {
			logger.Info("HTTP listener serving the stateless 2026-07-28 protocol; pre-2026 agents lose elicitation relay and list_changed updates")
		}
		if err := srv.RunHTTP(ctx, listen, stateless); err != nil && ctx.Err() == nil {
			return fmt.Errorf("mcp serve http: %w", err)
		}
		return nil
	}
	if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("mcp serve: %w", err)
	}
	return nil
}
