// Package buildinfo holds the identity of the running binary: the
// version, commit and build date the release build stamps in through
// ldflags. It is the one place those values live, so the --version
// flag, the admin system route and the User-Agent an outbound request
// presents all report the same build.
//
// It sits below every other package (it imports nothing first-party)
// because the outbound transport seam, internal/upstreamauth, needs the
// version and cannot import the server package that used to hold it.
package buildinfo

// Version is the release version, set at build time via ldflags. A
// build without one, such as `go run`, reports "dev".
var Version = "dev"

// Commit is the git short commit hash, set at build time via ldflags.
var Commit = "none"

// Date is the build timestamp, set at build time via ldflags.
var Date = "unknown"
