// The portal frontend is not Go, and this file is here to say so.
//
// A nested go.mod takes this directory out of the root module's package
// patterns, which fixes two things that both come from `./...` reaching into
// ui/node_modules:
//
//   - Parity. One npm dependency (flatted) ships a Go package inside
//     node_modules. CI's Test job never installs node modules, so `go test
//     ./...` there does not compile it; a developer's checkout does. The same
//     command was covering a different set of packages in the two places.
//   - A build race. `make verify` runs its lanes in parallel, and the
//     release-check lane's goreleaser hook runs `npm ci`, which deletes and
//     reinstalls ui/node_modules. When that landed while the Go lane was
//     compiling flatted, the run failed with `[build failed]` on a file that
//     had been deleted out from under the compiler — an intermittent failure
//     in third-party code that nothing in this repository imports.
//
// Serializing the lanes would have fixed the race and cost the parallelism
// `make verify` was built for; this removes the shared directory instead.
//
// Nothing imports anything from here, and nothing should. If first-party Go
// ever belongs to the frontend, it belongs in a package under the root module,
// not in this one.
module github.com/txn2/mcp-data-platform/ui

go 1.26.2
