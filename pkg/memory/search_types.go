package memory

// VectorQuery defines parameters for vector similarity search.
type VectorQuery struct {
	Embedding []float32
	Limit     int
	MinScore  float64
	Persona   string
	Status    string
}

// HybridQuery defines parameters for hybrid (vector + lexical) recall.
// Embedding drives the cosine arm; QueryText drives the lexical arm
// (Postgres full-text). A row matching either arm is a candidate; the
// two signals are fused per row by fuseHybridScore. Persona and Status
// are optional scope filters mirroring VectorQuery.
type HybridQuery struct {
	Embedding []float32
	QueryText string
	Limit     int
	Persona   string
	Status    string
}

// LexicalQuery defines parameters for lexical-only recall, used as the
// graceful-degradation path when no embedding provider is available.
// Unlike the vector arm, lexical search does not filter on a non-null
// embedding, so it also surfaces rows whose embedding was never
// computed (saved during an embedder outage).
type LexicalQuery struct {
	QueryText string
	Limit     int
	Persona   string
	Status    string
}

// ScoredRecord pairs a memory record with a similarity score.
type ScoredRecord struct {
	Record Record
	Score  float64
}
