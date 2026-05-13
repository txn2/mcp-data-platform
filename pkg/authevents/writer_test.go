package authevents

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestWriterNilSafe(_ *testing.T) {
	var w *Writer
	w.ConnectStarted(context.Background(), "mcp", "x", "u", "https://idp/token", "/back")
	w.RefreshSucceeded(context.Background(), "mcp", "x", "u", "https://idp/token", RefreshDetail{})
	// reaching here without panic is the assertion
}

func TestWriterIDPHostExtracted(t *testing.T) {
	s := NewMemoryStore()
	w := NewWriter(s, nil)
	w.ConnectStarted(context.Background(), "mcp", "x", "u",
		"https://idp.example.com/realms/test/protocol/openid-connect/token", "/back")
	got, _ := s.List(context.Background(), Filter{Kind: "mcp", Name: "x", Limit: 1})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].IDPHost != "idp.example.com" {
		t.Errorf("IDPHost = %q, want idp.example.com", got[0].IDPHost)
	}
}

func TestWriterRefreshDetailEncodesFields(t *testing.T) {
	s := NewMemoryStore()
	w := NewWriter(s, nil)
	before := time.Now().Add(-time.Hour).UTC()
	after := time.Now().Add(time.Hour).UTC()
	w.RefreshSucceeded(context.Background(), "api", "y", SystemBackgroundRefresh,
		"https://idp/token", RefreshDetail{
			BeforeExpiresAt: before,
			AfterExpiresAt:  after,
			RotatedRefresh:  true,
			DurationMS:      42,
		})
	got, _ := s.List(context.Background(), Filter{Kind: "api", Name: "y", Limit: 1})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	var d RefreshDetail
	if err := json.Unmarshal(got[0].Detail, &d); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if !d.RotatedRefresh || d.DurationMS != 42 {
		t.Errorf("detail = %+v", d)
	}
}

func TestWriterRefreshFailedClassesTag(t *testing.T) {
	s := NewMemoryStore()
	w := NewWriter(s, nil)
	w.RefreshFailedTransient(context.Background(), "api", "y", SystemBackgroundRefresh,
		"https://idp/token", RefreshDetail{DurationMS: 10})
	w.RefreshFailedRevoked(context.Background(), "api", "y", SystemBackgroundRefresh,
		"https://idp/token", RefreshDetail{IDPErrorCode: "invalid_grant"})

	got, _ := s.List(context.Background(), Filter{Kind: "api", Name: "y", Limit: 5})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	// got[0] is the most recent (revoked)
	var revoked RefreshDetail
	_ = json.Unmarshal(got[0].Detail, &revoked)
	if revoked.ErrorClass != "revoked" {
		t.Errorf("revoked event ErrorClass = %q, want revoked", revoked.ErrorClass)
	}
	var transient RefreshDetail
	_ = json.Unmarshal(got[1].Detail, &transient)
	if transient.ErrorClass != "transient" {
		t.Errorf("transient event ErrorClass = %q, want transient", transient.ErrorClass)
	}
}

func TestIdpHostOfEdgeCases(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"not a url":       "not a url",
		"https://h/p":     "h",
		"http://h:8080/p": "h:8080",
		"://broken":       "://broken",
	}
	for in, want := range cases {
		if got := idpHostOf(in); got != want {
			t.Errorf("idpHostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
