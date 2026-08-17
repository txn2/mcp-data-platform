// Package callindex is the call-catalog consumer of the shared indexjobs
// framework (#507, #1321). It registers a Source/Sink pair under
// source_kind = "calls" so a recorded call becomes findable by meaning and not
// only by the words it happens to contain.
//
// A call is recorded on the audit writer's goroutine, where embedding it would
// mean holding the writer open on a network call to the embedding provider, so
// the vector is produced off that path: the record lands without one and the
// reconciler converges it. Like memory, and unlike the api-catalog and tools
// consumers, the vector lives inline on the row it describes — a record IS its
// own indexing unit, so SourceID is the record id and each unit yields exactly
// one item.
package callindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "calls"
