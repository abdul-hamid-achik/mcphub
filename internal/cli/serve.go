package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	hubmcp "github.com/abdul-hamid-achik/mcphub/internal/mcp"
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
	var agentName string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the gateway MCP stdio server (proxies all enabled servers)",
		Long: `serve runs mcphub as a single MCP stdio server. It connects to every
enabled downstream server, aggregates their tools under 'server__tool' names,
and records each proxied call to the local intelligence db. Point your agents
at 'mcphub mcp serve' (gateway mode) to front them all with one connection.

When --agent <name> is given, the gateway applies that agent's ` + "`servers`" + ` /
` + "`tools`" + ` call scope plus its optional ` + "`pin`" + ` and ` + "`tool_schema_budget`" + `
advertisement policy from mcphub.yaml. A bare 'mcphub mcp serve' (no --agent)
is unscoped and uses the global exposure policy.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := loadConfig()
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

			// Logs go to stderr so they never corrupt the stdio JSON-RPC stream.
			logger := log.NewWithOptions(os.Stderr, log.Options{Prefix: "mcphub", ReportTimestamp: true})

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			h := hub.New(c, st, logger)
			srv := hubmcp.NewServer(c, h, st, scope)
			if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
				return fmt.Errorf("mcp serve: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name this gateway serves (applies servers/tools scope and pin/schema advertisement policy)")
	return cmd
}
