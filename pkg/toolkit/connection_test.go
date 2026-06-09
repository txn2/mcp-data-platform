package toolkit

import "testing"

// TestConnectionHealth_Wire verifies the wire conversion: nil passthrough, the
// RFC3339 UTC formatting of a positive unix time, and the omission of the
// success time when none has been recorded. This is the single formatter every
// operator surface (list_connections MCP tool, admin API) renders through, so
// the contract is asserted here rather than in each consumer.
func TestConnectionHealth_Wire(t *testing.T) {
	if got := (*ConnectionHealth)(nil).Wire(); got != nil {
		t.Errorf("nil health Wire() = %+v, want nil", got)
	}

	reachable := (&ConnectionHealth{
		Reachable:       true,
		LastSuccessUnix: 1_700_000_000,
	}).Wire()
	if reachable == nil {
		t.Fatal("Wire() = nil for reachable health")
	}
	if !reachable.Reachable {
		t.Error("Reachable = false, want true")
	}
	if reachable.LastSuccess != "2023-11-14T22:13:20Z" {
		t.Errorf("LastSuccess = %q, want RFC3339 UTC", reachable.LastSuccess)
	}
	if reachable.LastError != "" {
		t.Errorf("LastError = %q, want empty", reachable.LastError)
	}

	down := (&ConnectionHealth{
		Reachable: false,
		LastError: "dial tcp: connection refused",
	}).Wire()
	if down == nil {
		t.Fatal("Wire() = nil for unreachable health")
	}
	if down.Reachable {
		t.Error("Reachable = true, want false")
	}
	if down.LastSuccess != "" {
		t.Errorf("LastSuccess = %q, want empty when never succeeded", down.LastSuccess)
	}
	if down.LastError != "dial tcp: connection refused" {
		t.Errorf("LastError = %q", down.LastError)
	}
}

// TestConnectionDetailFields verifies the ConnectionDetail struct fields.
func TestConnectionDetailFields(t *testing.T) {
	d := ConnectionDetail{
		Name:        "warehouse",
		Description: "Data warehouse",
		IsDefault:   true,
	}
	if d.Name != "warehouse" {
		t.Errorf("Name = %q", d.Name)
	}
	if d.Description != "Data warehouse" {
		t.Errorf("Description = %q", d.Description)
	}
	if !d.IsDefault {
		t.Error("IsDefault = false")
	}
}

// TestConnectionListerInterface is a compile-time check that the interface is usable.
func TestConnectionListerInterface(t *testing.T) {
	var _ ConnectionLister = mockLister{}
	t.Log("ConnectionLister interface is satisfiable")
}

type mockLister struct{}

func (mockLister) ListConnections() []ConnectionDetail {
	return []ConnectionDetail{
		{Name: "test", Description: "test desc", IsDefault: true},
	}
}
