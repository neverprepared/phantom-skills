// Package version exposes build metadata for pskillctl.
//
// Values are stamped at link time via -ldflags:
//
//	go build -ldflags "-X github.com/neverprepared/phantom-skills/internal/version.Version=v0.1.0-dev \
//	                   -X github.com/neverprepared/phantom-skills/internal/version.Commit=$(git rev-parse --short HEAD) \
//	                   -X github.com/neverprepared/phantom-skills/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
//	    ./cmd/pskillctl
package version

var (
	// Version is the semantic version of pskillctl (e.g. v0.1.0-dev).
	Version = "v0.1.0-dev"

	// Commit is the short git SHA the binary was built from.
	Commit = "unknown"

	// BuildDate is RFC3339 UTC.
	BuildDate = "unknown"
)
