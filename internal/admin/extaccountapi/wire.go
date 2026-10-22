package extaccountapi

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/extaccount"
)

// The bodies the routes read and write.

// UserView is one external account as the admin list shows it.
type UserView struct {
	extaccount.Account
	// Status is active, suspended, expired or converted.
	Status string `json:"status" example:"active"`
	// SharedItems is the live shares to the account's address.
	SharedItems int `json:"shared_items" example:"3"`
}

// UserList is the admin list.
type UserList struct {
	Users []UserView `json:"users"`
}

// ExtendRequest sets or clears an account's expiry.
type ExtendRequest struct {
	// ExpiresAt is the new expiry; null removes it.
	ExpiresAt *time.Time `json:"expires_at"`
}

// LinkRequest asks for a sign-in link.
type LinkRequest struct {
	Email string `json:"email" example:"client@example.org"`
}
