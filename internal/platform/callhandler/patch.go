package callhandler

// Patch is a change to a handler's match and settings: a nil field is left
// as it is, so handler_set naming a handler changes only what it sends.
type Patch struct {
	Connection *string
	Operations *[]string
	Personas   *[]string
	Users      *[]string
	// Everyone applies the handler to every caller, the explicit choice an
	// empty persona and user list needs (#2650); true clears both lists.
	Everyone   *bool
	Conditions *[]Condition
	Args       map[string]any
	TimeoutMS  *int
	OnFailure  *string
	ItemsPath  *string
	Writes     *map[string]bool
	Protected  *[]string
	Order      *int
}

// Apply writes p's fields onto h.
func (p Patch) Apply(h *Handler) {
	setIf(&h.Connection, p.Connection)
	setIf(&h.Operations, p.Operations)
	setIf(&h.Personas, p.Personas)
	setIf(&h.Users, p.Users)
	if p.Everyone != nil && *p.Everyone {
		h.Personas, h.Users = nil, nil
	}
	setIf(&h.Conditions, p.Conditions)
	setIf(&h.TimeoutMS, p.TimeoutMS)
	setIf(&h.OnFailure, p.OnFailure)
	setIf(&h.ItemsPath, p.ItemsPath)
	setIf(&h.Writes, p.Writes)
	setIf(&h.Protected, p.Protected)
	setIf(&h.Order, p.Order)
	if p.Args != nil {
		h.Args = p.Args
	}
}

func setIf[T any](dst, v *T) {
	if v != nil {
		*dst = *v
	}
}

// CheckWho refuses a handler p leaves applying to every caller when nobody
// chose that (#2650): a new one with no personas, no people and no everyone,
// and a change that empties its lists without everyone. A change that leaves
// who it applies to alone keeps it. everyone with personas or people is a
// contradiction.
func (p Patch) CheckWho(h *Handler, adding bool) error {
	chose := p.Everyone != nil && *p.Everyone
	if chose && p.namesAnyone() {
		return invalid("everyone applies the %s to every caller, so it takes no personas or users", h.Kind)
	}
	touched := p.Personas != nil || p.Users != nil || p.Everyone != nil
	if !h.Everyone() || chose || (!adding && !touched) {
		return nil
	}
	return invalid("name the personas or users the %s applies to, or set everyone to true to apply it to every caller "+
		"on the connection: an empty list is not taken to mean everyone", h.Kind)
}

// namesAnyone reports whether p names a persona or a person.
func (p Patch) namesAnyone() bool {
	return (p.Personas != nil && len(*p.Personas) > 0) || (p.Users != nil && len(*p.Users) > 0)
}
