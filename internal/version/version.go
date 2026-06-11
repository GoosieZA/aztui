// Package version holds build metadata, overridden at link time via
// -ldflags "-X github.com/GoosieZA/aztui/internal/version.Version=v0.2.0".
package version

var (
	Version = "v0.1.0-dev"
	Commit  = "unknown"
)
