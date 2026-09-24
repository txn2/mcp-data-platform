package upstreamretry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFromResult(t *testing.T) {
	assert.Equal(t, Seen{}, FromResult(map[string]any{"status": float64(429)}), "no advice is no retry")
	assert.Equal(t, Seen{Retryable: true, Status: 429, After: 7 * time.Second},
		FromResult(map[string]any{"upstream_retryable": true, "retry_after_seconds": float64(7), "upstream_status": float64(429)}))
	assert.Equal(t, Seen{Retryable: true, Status: 503},
		FromResult(map[string]any{"upstream_retryable": true, "status": float64(503)}))
}

func TestSeen_Wait(t *testing.T) {
	named := Seen{Retryable: true, After: 3 * time.Second}
	unnamed := Seen{Retryable: true}
	cases := map[string]struct {
		advice    Seen
		retry     int
		remaining time.Duration
		want      time.Duration
		ok        bool
	}{
		"not retryable":                  {Seen{}, 0, time.Hour, 0, false},
		"the named interval":             {named, 0, time.Hour, 3 * time.Second, true},
		"a doubling wait when unnamed":   {unnamed, 2, time.Hour, 4 * time.Second, true},
		"the retries are spent":          {unnamed, MaxRetries, time.Hour, 0, false},
		"past the run's deadline":        {named, 0, 2 * time.Second, 0, false},
		"longer than the host will wait": {Seen{Retryable: true, After: 2 * time.Minute}, 0, time.Hour, 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := tc.advice.Wait(tc.retry, tc.remaining)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestSeen_Answer(t *testing.T) {
	assert.Equal(t, "429 Too Many Requests", Seen{Status: 429}.Answer())
	assert.Equal(t, "a refusal to retry later", Seen{}.Answer())
}
