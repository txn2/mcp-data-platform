package portalstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFuseHybridScore(t *testing.T) {
	// cosine 1.0 mapped to semantic 1.0, lexical match -> 0.6*1 + 0.4*1 = 1.0
	assert.InDelta(t, 1.0, fuseHybridScore(1.0, true), 1e-9)
	// cosine 1.0, no lexical match -> 0.6*1 + 0.4*0 = 0.6
	assert.InDelta(t, 0.6, fuseHybridScore(1.0, false), 1e-9)
	// cosine 0.0 maps to semantic 0.5; with lexical match -> 0.6*0.5 + 0.4 = 0.7
	assert.InDelta(t, 0.7, fuseHybridScore(0.0, true), 1e-9)
}
