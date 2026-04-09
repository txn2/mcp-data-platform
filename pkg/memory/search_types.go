package memory

// VectorQuery defines parameters for vector similarity search.
type VectorQuery struct {
	Embedding []float32
	Limit     int
	MinScore  float64
	Persona   string
	Status    string
}

// ScoredRecord pairs a memory record with a similarity score.
type ScoredRecord struct {
	Record Record
	Score  float64
}
