// Package buildinfo identifies the exact image served by health checks.
package buildinfo

// Set by Docker's build stage. Local go builds intentionally report development values.
var Version = "dev"
var Revision = "unknown"
