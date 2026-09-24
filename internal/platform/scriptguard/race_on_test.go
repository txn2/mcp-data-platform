//go:build race

package scriptguard

// raceEnabled is set under the race detector, whose instrumentation changes
// what an allocation costs.
const raceEnabled = true
