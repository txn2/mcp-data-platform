package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

type stubAuthenticator struct {
	info *middleware.UserInfo
	err  error
}

func (s *stubAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return s.info, s.err
}

func TestObservingAuthenticator_ObservesOnSuccess(t *testing.T) {
	want := &middleware.UserInfo{UserID: "u1", Email: "a@b.io", AuthType: "oidc"}
	var seen *middleware.UserInfo
	a := NewObservingAuthenticator(
		&stubAuthenticator{info: want},
		func(info *middleware.UserInfo) { seen = info },
	)

	got, err := a.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("returned info = %v, want %v", got, want)
	}
	if seen != want {
		t.Errorf("observer saw %v, want %v", seen, want)
	}
}

func TestObservingAuthenticator_SkipsObserveOnError(t *testing.T) {
	called := false
	a := NewObservingAuthenticator(
		&stubAuthenticator{err: errors.New("denied")},
		func(*middleware.UserInfo) { called = true },
	)

	if _, err := a.Authenticate(context.Background()); err == nil {
		t.Fatal("expected error to propagate")
	}
	if called {
		t.Error("observer must not run on authentication failure")
	}
}

func TestObservingAuthenticator_NilObserver(t *testing.T) {
	a := NewObservingAuthenticator(&stubAuthenticator{info: &middleware.UserInfo{}}, nil)
	if _, err := a.Authenticate(context.Background()); err != nil {
		t.Fatalf("nil observer must be safe: %v", err)
	}
}
