package inprocess

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTrack(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v1"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	if Is(ss) {
		t.Fatal("an untracked session reads as in-process")
	}
	untrack := Track(ss)
	if !Is(ss) {
		t.Fatal("a tracked session does not read as in-process")
	}
	untrack()
	if Is(ss) {
		t.Fatal("an untracked session still reads as in-process")
	}
	Track(nil)()
	if Is(nil) {
		t.Fatal("nil reads as in-process")
	}
}
