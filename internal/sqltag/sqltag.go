// Package sqltag is the comment that opens every statement the platform runs
// on an application database for its own bookkeeping (#2528): the type
// lookups a call makes, describe, plan, the own-database guard, the outbox
// poll and the health report. The health report's ranking of statements
// leaves out every statement carrying it, so what it ranks is the
// application's own work. A comment changes neither the plan nor
// pg_stat_statements' query id.
//
// The statements write the comment into their text rather than concatenate
// this constant, so each stays one literal the statement gate can read; a
// test in each package holds every one to it.
package sqltag

// Platform is the comment.
const Platform = "/* mcp-data-platform */"
