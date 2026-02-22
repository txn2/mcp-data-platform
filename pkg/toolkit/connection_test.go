package toolkit

import "testing"

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
