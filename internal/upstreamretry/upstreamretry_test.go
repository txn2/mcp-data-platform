package upstreamretry

import (
	"net/http"
	"testing"
	"time"
)

func TestAfter(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"2", 2 * time.Second, true},
		{" 0 ", 0, true},
		{"-1", 0, false},
		{"", 0, false},
		{"soon", 0, false},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	}
	for _, tc := range cases {
		got, ok := After(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("After(%q) = (%s, %v); want (%s, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		method string
		status int
		want   bool
	}{
		{http.MethodGet, http.StatusTooManyRequests, true},
		{http.MethodPost, http.StatusTooManyRequests, true},
		{"get", http.StatusServiceUnavailable, true},
		{http.MethodHead, http.StatusServiceUnavailable, true},
		{http.MethodPost, http.StatusServiceUnavailable, false},
		{http.MethodGet, http.StatusBadGateway, false},
		{http.MethodGet, http.StatusOK, false},
		{http.MethodGet, http.StatusNotFound, false},
	}
	for _, tc := range cases {
		if got := Retryable(tc.method, tc.status); got != tc.want {
			t.Errorf("Retryable(%s, %d) = %v; want %v", tc.method, tc.status, got, tc.want)
		}
	}
}

func TestAdvise(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if got := Advise(http.MethodGet, http.StatusOK, http.Header{"Retry-After": {"5"}}, now); got != (Advice{}) {
		t.Errorf("a 200 carries advice %+v", got)
	}
	if got := Advise(http.MethodGet, http.StatusTooManyRequests, http.Header{}, now); got != (Advice{Retryable: true}) {
		t.Errorf("a 429 with no Retry-After = %+v; want retryable with no interval", got)
	}
	got := Advise(http.MethodGet, http.StatusTooManyRequests,
		http.Header{"Retry-After": {now.Add(1500 * time.Millisecond).Format(http.TimeFormat)}}, now)
	if got != (Advice{Retryable: true, RetryAfterSeconds: 1}) {
		t.Errorf("a dated Retry-After = %+v; want the interval rounded up to whole seconds", got)
	}
	if got := Advise(http.MethodPost, http.StatusServiceUnavailable, http.Header{"Retry-After": {"3"}}, now); got != (Advice{}) {
		t.Errorf("a POST answered 503 carries advice %+v; a write is not repeated", got)
	}
	if got := Advise(http.MethodGet, http.StatusServiceUnavailable, http.Header{"Retry-After": {"3"}}, now); got != (Advice{Retryable: true, RetryAfterSeconds: 3}) {
		t.Errorf("a GET answered 503 = %+v", got)
	}
}
