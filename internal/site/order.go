package site

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Place is where an environment goes in a site's order (#2617): immediately
// after another one. dev is always first and prod always last, so every place
// there is lies after dev or after an environment between them, and nothing
// goes after prod.
type Place struct {
	After string `json:"after"`
}

// Placed is order with name put at p: added when order lacks it, moved when it
// holds it. A nil p puts a new environment last before prod and leaves an
// existing one where it is. prod always goes last and dev first, whatever p
// says.
func Placed(order []string, name string, p *Place) ([]string, error) {
	rest := slices.DeleteFunc(slices.Clone(order), func(n string) bool { return n == name })
	switch {
	case name == EnvDev:
		return append([]string{EnvDev}, rest...), nil
	case name == EnvProd:
		return append(rest, EnvProd), nil
	case p == nil && slices.Contains(order, name):
		return slices.Clone(order), nil
	case p == nil:
		return insertAt(rest, name, beforeProd(rest)), nil
	}
	at, err := placeIndex(rest, name, *p)
	if err != nil {
		return nil, err
	}
	return insertAt(rest, name, at), nil
}

// placeIndex is the index in rest a place names: the one after its
// environment.
func placeIndex(rest []string, name string, p Place) (int, error) {
	switch p.After {
	case "":
		return 0, fmt.Errorf("say which environment %s goes after", name)
	case name:
		return 0, fmt.Errorf("environment %s cannot go after itself", name)
	case EnvProd:
		return 0, errors.New("prod is always last: nothing goes after it")
	}
	i := slices.Index(rest, p.After)
	if i < 0 {
		return 0, fmt.Errorf("environment %q: %w", p.After, ErrNoEnvironment)
	}
	return i + 1, nil
}

func beforeProd(order []string) int {
	if i := slices.Index(order, EnvProd); i >= 0 {
		return i
	}
	return len(order)
}

func insertAt(order []string, name string, at int) []string {
	return slices.Insert(slices.Clone(order), at, name)
}

// PlaceChoices is the places a new environment, or one being moved, can go in
// an order: after each environment but prod and itself. A single choice means
// there is nowhere else for it.
func PlaceChoices(order []string, name string) []Place {
	out := []Place{}
	for _, n := range order {
		if n != EnvProd && n != name {
			out = append(out, Place{After: n})
		}
	}
	if len(out) == 0 {
		out = append(out, Place{After: EnvDev})
	}
	return out
}

// String is a place as a sentence names it.
func (p Place) String() string { return "after " + p.After }

// PlaceRefusal is the refusal of a new environment whose place is not said
// when there is more than one: the environments it could go after.
func PlaceRefusal(name string, choices []Place) string {
	names := make([]string, 0, len(choices))
	for _, c := range choices {
		names = append(names, c.After)
	}
	list := strings.Join(names, ", ")
	if n := len(names); n > 1 {
		list = strings.Join(names[:n-1], ", ") + " or " + names[n-1]
	}
	return fmt.Sprintf("say which environment %s goes after: %s", name, list)
}

// PlaceNew is where a new environment goes: the place asked for, or nil for
// the one place there is, refusing a choice left unsaid when there are more.
func PlaceNew(order []string, name string, p *Place) (*Place, error) {
	if p == nil {
		if choices := PlaceChoices(order, name); len(choices) > 1 && name != EnvProd {
			return nil, errors.New(PlaceRefusal(name, choices))
		}
		return nil, nil //nolint:nilnil // nil place: last before prod, the one place there is
	}
	if _, err := Placed(order, name, p); err != nil {
		return nil, err
	}
	return p, nil
}

// PassedGated is an environment requiring approval that a move puts name
// after when it was before it, "" for none: what name serves would then feed
// the environments the gated one guards.
func (s *Site) PassedGated(before, after []string, name string) string {
	from, to := slices.Index(before, name), slices.Index(after, name)
	if to <= from {
		return ""
	}
	for _, n := range before[from+1:] {
		if e := s.Environment(n); e != nil && e.RequiresApproval && slices.Index(after, n) < to {
			return n
		}
	}
	return ""
}

// Previous is the environment immediately before name in the site's order,
// nil for dev or an environment the site does not have.
func (s *Site) Previous(name string) *Environment {
	for i := range s.Environments {
		if s.Environments[i].Name == name {
			if i == 0 {
				return nil
			}
			return &s.Environments[i-1]
		}
	}
	return nil
}

// Order is the names of the site's environments in its order.
func (s *Site) Order() []string {
	out := make([]string, 0, len(s.Environments))
	for _, e := range s.Environments {
		out = append(out, e.Name)
	}
	return out
}
