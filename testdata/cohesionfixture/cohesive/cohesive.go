// Package cohesive is a cohesion-gate test fixture: independent handlers that
// all reference one shared Store, so the shared-identifier edge makes them a
// single connected cluster (the "handlers over one Store" case the gate must
// NOT flag). Loaded explicitly by TestCohesionAcceptsSharedType.
package cohesive

// Store is the shared package-level identifier every handler references.
type Store struct{ n int }

func NewStore() *Store { return &Store{} }

// CreateHandler, ReadHandler and DeleteHandler never call each other; they
// cohere only by both operating on *Store.
func CreateHandler(s *Store) { s.n++ }

func ReadHandler(s *Store) int { return s.n }

func DeleteHandler(s *Store) { s.n = 0 }

func countRows(s *Store) int { return s.n }

// StatsHandler is a fifth declaration to clear minSignificantCluster.
func StatsHandler(s *Store) int { return countRows(s) }
