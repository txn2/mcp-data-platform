package tablesapi

import (
	"github.com/txn2/mcp-data-platform/internal/platform/tablereconcile"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
)

// The bodies the routes read and write.

// RegistrationList is a page of registrations and the total the filter
// matched.
type RegistrationList struct {
	Registrations []tableregister.Registration `json:"registrations"`
	Total         int                          `json:"total"`
}

// RepairRequest is the body of a repair.
type RepairRequest struct {
	Connection string                      `json:"connection"`
	Repair     []tablereconcile.RepairItem `json:"repair"`
}

// RepairResponse is what each repair did.
type RepairResponse struct {
	Outcomes []tablereconcile.RepairOutcome `json:"outcomes"`
}
