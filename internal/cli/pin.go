package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
)

func newPinCmd() *cobra.Command {
	var top int
	var since string
	cmd := &cobra.Command{
		Use:   "pin [server | server__tool | server__* ...]",
		Short: "Pin tools so they stay directly callable even in lazy mode",
		Long: `pin keeps tools mounted directly on the gateway even under expose:lazy, so
your agents call them automatically instead of going through
mcphub_search_tools first.

A pin can be a whole server (pins all its tools), a wildcard, or a single tool:

  mcphub pin codemap vecgrep              # whole servers
  mcphub pin codemap__*                   # same, explicit wildcard
  mcphub pin codemap__semantic            # one tool
  mcphub pin --top 8                      # auto-pin your 8 most-called tools (from stats)
  mcphub pin --top 8 --since 7d           # same, last 7 days only
  mcphub pin                              # list current pins

A running gateway hot-reloads mcphub.yaml within a few seconds. Otherwise
restart the agent (or mcphub up) to pick pins up.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// No args and no --top: list current pins.
			if top == 0 && len(args) == 0 {
				if len(c.Pin) == 0 {
					fmt.Fprintln(out, "No pins. Pin a server with `mcphub pin <server>` (or `--top N`).")
					return nil
				}
				for _, p := range c.Pin {
					fmt.Fprintln(out, p)
				}
				return nil
			}

			toAdd := append([]string{}, args...)
			if top > 0 {
				window, err := parseSince(since)
				if err != nil {
					return err
				}
				st, err := openStore()
				if err != nil {
					return err
				}
				defer st.Close()
				tools, err := st.ToolStatsSince(context.Background(), window)
				if err != nil {
					return err
				}
				if len(tools) == 0 {
					fmt.Fprintln(out, "No recorded tool calls yet — nothing to auto-pin. Use the gateway, then retry.")
				}
				picked := 0
				for _, t := range tools {
					if picked >= top {
						break
					}
					if _, ok := c.Servers[t.Server]; !ok {
						continue
					}
					toAdd = append(toAdd, t.Server+"__"+hub.PublicToolName(t.Server, t.Tool))
					picked++
				}
			}

			existing := map[string]bool{}
			for _, p := range c.Pin {
				existing[p] = true
			}
			added := 0
			for _, p := range toAdd {
				if !existing[p] {
					c.Pin = append(c.Pin, p)
					existing[p] = true
					added++
				}
			}
			sort.Strings(c.Pin)
			// Save validates — an unknown server in a pin is rejected here.
			if err := config.Save(path, c); err != nil {
				return err
			}
			fmt.Fprintf(out, "Pinned %d (now %d total). A running gateway reloads within a few seconds.\n", added, len(c.Pin))
			return nil
		},
	}
	cmd.Flags().IntVar(&top, "top", 0, "auto-pin the N most-called tools from the intelligence store")
	cmd.Flags().StringVar(&since, "since", "", "with --top, only count calls in this window (e.g. 7d, 24h)")
	return cmd
}

func newUnpinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpin <server | tool ...>",
		Short: "Remove pins",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			// A bare arg (no "__") removes every pin that resolves to that server;
			// a namespaced arg removes that exact pin.
			bare := map[string]bool{}
			exact := map[string]bool{}
			for _, a := range args {
				if strings.Contains(a, "__") {
					exact[a] = true
				} else {
					bare[a] = true
				}
			}
			matched := map[string]bool{}
			kept := c.Pin[:0]
			removed := 0
			for _, p := range c.Pin {
				switch {
				case exact[p]:
					matched[p] = true
					removed++
				case bare[config.PinServer(p)]:
					matched[config.PinServer(p)] = true
					removed++
				default:
					kept = append(kept, p)
				}
			}
			c.Pin = kept
			if err := config.Save(path, c); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, a := range args {
				if !matched[a] {
					fmt.Fprintf(out, "No pin matched %q.\n", a)
				}
			}
			fmt.Fprintf(out, "Unpinned %d (now %d total).\n", removed, len(c.Pin))
			return nil
		},
	}
}
