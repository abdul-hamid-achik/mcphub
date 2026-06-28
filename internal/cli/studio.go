package cli

import (
	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/ui/studio"
)

func newStudioCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "studio",
		Aliases: []string{"tui"},
		Short:   "Launch the interactive TUI to register/offload servers",
		Long: `studio is mcphub's interactive terminal UI. Browse your servers, toggle
them on and off with space, and inspect local usage intelligence — then run
'mcphub sync' to push the result to every agent.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			return studio.Run(c, path, st)
		},
	}
}
