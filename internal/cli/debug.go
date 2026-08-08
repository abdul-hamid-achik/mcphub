package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/logsink"
	"github.com/abdul-hamid-achik/mcphub/internal/version"
)

func newDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug <subcommand>",
		Short: "Diagnostics for sharing and post-mortems",
	}
	cmd.AddCommand(newDebugBundleCmd())
	return cmd
}

// newDebugBundleCmd assembles everything a post-mortem needs — recent gateway
// log files, `doctor` output, recent call telemetry, the build version — and
// stashes it with fcheap so a single id can be handed to whoever is
// debugging. The config file is deliberately NOT included: headers may hold
// literal credentials when someone has not moved them to tvault:// yet, and a
// debug artifact must be safe to share by construction.
func newDebugBundleCmd() *cobra.Command {
	var (
		days   int
		ttl    string
		noSave bool
	)
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Collect logs, doctor output, and telemetry into a shareable fcheap stash",
		Long: `bundle gathers the last days of gateway log files, the current doctor
report, recent call telemetry, and the build version into one directory, then
saves it with fcheap (tagged mcphub-debug) so the stash id can be shared.

The mcphub.yaml config is intentionally not included — it can carry literal
credentials in server headers. fcheap additionally runs its save-time secret
scan over the bundle.

Requires fcheap on PATH for the save step; --no-save skips it and prints the
bundle directory instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.MkdirTemp("", "mcphub-debug-*")
			if err != nil {
				return err
			}

			logCount := copyRecentLogs(logsink.DefaultDir(), dir, days)

			exe, err := os.Executable()
			if err != nil {
				return err
			}
			// Shelling out to the same binary reuses doctor's and stats'
			// output formats exactly as a human would see them. doctor exits
			// non-zero when it finds problems — that is a finding for the
			// bundle, not a failure of the bundle.
			doctorOut, _ := exec.Command(exe, "doctor").CombinedOutput()
			writeBundleFile(dir, "doctor.txt", doctorOut)
			statsOut, _ := exec.Command(
				exe, "stats", "--recent", "200", "--since", fmt.Sprintf("%dd", days), "--markdown",
			).CombinedOutput()
			writeBundleFile(dir, "stats.md", statsOut)
			writeBundleFile(dir, "version.txt", []byte(fmt.Sprintf(
				"mcphub %s (commit %s, built %s)\n%s/%s\ncollected %s\nlog files included: %d (last %d days)\n",
				version.Version, version.Commit, version.Date,
				runtime.GOOS, runtime.GOARCH,
				time.Now().UTC().Format(time.RFC3339), logCount, days,
			)))

			if noSave {
				fmt.Fprintf(cmd.OutOrStdout(), "bundle written to %s (not saved: --no-save)\n", dir)
				return nil
			}
			if _, err := exec.LookPath("fcheap"); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(),
					"bundle written to %s\nfcheap is not on PATH, so it was not stashed — install fcheap or share the directory directly.\n", dir)
				return nil
			}
			save := exec.Command(
				"fcheap", "save", dir,
				"--tool", "mcphub",
				"--tag", "mcphub-debug",
				"--ttl", ttl,
				"--index",
				"--name", "mcphub debug bundle "+time.Now().Format("2006-01-02 15:04"),
			)
			save.Stdout = cmd.OutOrStdout()
			save.Stderr = cmd.ErrOrStderr()
			if err := save.Run(); err != nil {
				return fmt.Errorf("fcheap save: %w (bundle remains at %s)", err, dir)
			}
			// The stash owns the content now; the temp copy has no second use.
			_ = os.RemoveAll(dir)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 3, "how many days of logs and telemetry to include")
	cmd.Flags().StringVar(&ttl, "ttl", "30d", "fcheap stash time-to-live")
	cmd.Flags().BoolVar(&noSave, "no-save", false, "assemble the bundle directory but skip the fcheap save")
	return cmd
}

// copyRecentLogs copies log files modified within the window into dst/logs,
// returning how many made it. Missing directories and unreadable files are
// skipped: a partial bundle beats no bundle.
func copyRecentLogs(logDir, dst string, days int) int {
	if logDir == "" {
		return 0
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return 0
	}
	outDir := filepath.Join(dst, "logs")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	copied := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		if copyFile(filepath.Join(logDir, name), filepath.Join(outDir, name)) == nil {
			copied++
		}
	}
	return copied
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeBundleFile(dir, name string, content []byte) {
	_ = os.WriteFile(filepath.Join(dir, name), content, 0o600)
}
