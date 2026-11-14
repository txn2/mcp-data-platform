package callhandler

// Patch is a change to a handler's match and settings: a nil field is left
// as it is, so handler_set naming a handler changes only what it sends.
type Patch struct {
	Connection *string
	Operations *[]string
	Personas   *[]string
	Users      *[]string
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
