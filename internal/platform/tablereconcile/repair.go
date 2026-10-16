package tablereconcile

// RepairItem is one repair an administrator asks for: "drop" an orphan table
// by name, or "forget" a registration whose table is gone by id.
type RepairItem struct {
	Action         string `json:"action" enums:"drop,forget"`
	Table          string `json:"table,omitempty"`
	RegistrationID string `json:"registration_id,omitempty"`
}

// The repair actions.
const (
	ActionDrop   = "drop"
	ActionForget = "forget"
)

// RepairOutcome is what one repair did.
type RepairOutcome struct {
	RepairItem
	// Done is whether it was carried out; Reason says why when it was not.
	Done   bool   `json:"done"`
	Reason string `json:"reason,omitempty"`
}
