package persona

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestDiffersNamesEveryArea: a block that differs from another in one area
// alone is reported under that area's name, for every area of PortalAccess
// (#2718: sites was left out, so the external limits turned the Sites area
// off without saying so). Uploads is the one area the limits leave as set,
// and External is reported, never stored.
func TestDiffersNamesEveryArea(t *testing.T) {
	typ := reflect.TypeFor[PortalAccess]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "external" || name == "uploads" {
			continue
		}
		var stored, effective PortalAccess
		sv, ev := reflect.ValueOf(&stored).Elem().Field(i), reflect.ValueOf(&effective).Elem().Field(i)
		switch f.Type.Kind() {
		case reflect.String:
			sv.SetString("a")
			ev.SetString("b")
		case reflect.Pointer:
			on, off := true, false
			sv.Set(reflect.ValueOf(&on))
			ev.Set(reflect.ValueOf(&off))
		default:
			t.Fatalf("field %s has a kind Differs does not compare: %s", f.Name, f.Type.Kind())
		}
		if got := Differs(stored, effective); !slices.Equal(got, []string{name}) {
			t.Errorf("a difference in %s alone is reported as %v, want [%s]", f.Name, got, name)
		}
	}
}

// An external persona that stores sites on is reported as capped there.
func TestDiffers_ExternalSites(t *testing.T) {
	on := true
	p := &Persona{External: true, Portal: &PortalAccess{Sites: &on}}
	if got := Differs(p.StoredPortal(nil), p.PortalPermissions(nil)); !slices.Contains(got, "sites") {
		t.Fatalf("an external persona storing sites on reports %v; want sites among the capped areas", got)
	}
}
