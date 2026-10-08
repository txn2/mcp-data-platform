package datahub

import (
	"context"
	"errors"
	"testing"
)

func TestAdapter_Ping(t *testing.T) {
	down := errors.New("connection refused")
	mock := &mockDataHubClient{}
	adapter, err := NewWithClient(Config{}, mock)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Ping(context.Background()); err != nil {
		t.Errorf("Ping = %v", err)
	}
	mock.pingFunc = func(context.Context) error { return down }
	if err := adapter.Ping(context.Background()); !errors.Is(err, down) {
		t.Errorf("Ping = %v, want the client's error", err)
	}
}
