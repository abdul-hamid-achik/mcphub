// Package version holds build metadata stamped in at link time.
package version

// These are overridden via -ldflags by the Taskfile's release/install tasks:
//
//	-X github.com/abdul-hamid-achik/mcphub/internal/version.Version=v0.1.0
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
