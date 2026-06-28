// Package cli wires mcphub's cobra command tree. Every view is a single-shot
// command (list, doctor, stats, sync) plus the long-running `studio` TUI and
// `mcp serve` gateway, so humans, scripts, and agents share one surface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/version"
)

var (
	flagConfig string
	flagDB     string
	flagJSON   bool
)

// Root builds the top-level `mcphub` command.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "mcphub",
		Short: "One hub for all your MCP servers, synced into every agent",
		Long: `mcphub is a gateway and control plane for Model Context Protocol servers.

Define your servers once in mcphub.yaml (or the Studio TUI), and:

  • mcphub mcp serve   runs a single gateway that proxies them all, so each
                       agent connects to ONE server instead of a dozen.
  • mcphub sync        writes the right config into every agent harness
                       (Claude Code, opencode, Codex, ...), so you stop
                       hand-editing each one.
  • mcphub studio      a TUI to register/offload servers and watch usage.

Every proxied tool call is recorded locally so 'mcphub stats' can show which
servers actually earn their place in your context window.`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version.Version, version.Commit, version.Date),
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.SetVersionTemplate("mcphub {{.Version}}\n")
	root.PersistentFlags().StringVar(&flagConfig, "config", "", "path to mcphub.yaml (default: ./mcphub.yaml or ~/.config/mcphub/mcphub.yaml)")
	root.PersistentFlags().StringVar(&flagDB, "db", "", "path to the intelligence SQLite db (default: ~/.local/share/mcphub/mcphub.db)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON where supported")

	root.AddCommand(
		newInitCmd(),
		newListCmd(),
		newAddCmd(),
		newRemoveCmd(),
		newEnableCmd(),
		newDisableCmd(),
		newGroupsCmd(),
		newUseCmd(),
		newSyncCmd(),
		newStudioCmd(),
		newStatusCmd(),
		newStatsCmd(),
		newDoctorCmd(),
		newMCPCmd(),
	)
	return root
}

// Execute runs the root command.
func Execute() {
	if err := Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// configPath resolves the config path from the flag or defaults.
func configPath() string {
	if flagConfig != "" {
		return flagConfig
	}
	return config.DefaultPath()
}

// dbPath resolves the db path from the flag or defaults.
func dbPath() string {
	if flagDB != "" {
		return flagDB
	}
	return store.DefaultPath()
}

// loadConfig loads the config, returning a helpful error if it is missing.
func loadConfig() (*config.Config, string, error) {
	path := configPath()
	c, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(err) || os.IsNotExist(rootCause(err)) {
			return nil, path, fmt.Errorf("no config at %s — run `mcphub init` to create one", path)
		}
		return nil, path, err
	}
	return c, path, nil
}

// openStore opens the intelligence db.
func openStore() (*store.Store, error) {
	return store.Open(dbPath())
}

func rootCause(err error) error {
	type unwrapper interface{ Unwrap() error }
	for {
		u, ok := err.(unwrapper)
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}
