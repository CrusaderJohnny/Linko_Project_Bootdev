// Package build houses build-time variables set via -ldflags.
package build

// Build-time variables. These must be strings.
var (
	GitSHA    = "unknown"
	BuildTime = "unknown"
)
