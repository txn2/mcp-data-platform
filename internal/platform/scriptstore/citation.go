package scriptstore

import (
	"context"

	"github.com/google/uuid"
)

// Citation is what a knowledge page's citation of a script needs when it is
// resolved for a reader (#1855): the name the script is shown by, and the owner
// who, with an administrator, may open it.
type Citation struct {
	// Label is the script's display name, or its name when it has none.
	Label string
	// Owner is the one person the script belongs to; empty for a script that
	// is nobody's until an administrator transfers it.
	Owner string
}

// Citation returns the cited script's label and owner, or nil, nil when no
// script has this id. An id that is not a UUID names no script (scripts.id
// cannot hold one), so it is answered as not found without a query rather than
// handed to the database to fail on.
func (s *Store) Citation(ctx context.Context, id string) (*Citation, error) {
	if !validUUID(id) {
		return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
	}
	sc, err := s.GetByID(ctx, id)
	if err != nil || sc == nil {
		return nil, err
	}
	label := sc.DisplayName
	if label == "" {
		label = sc.Name
	}
	return &Citation{Label: label, Owner: sc.OwnerEmail}, nil
}

// validUUID reports whether id is a UUID, the only thing scripts.id holds.
func validUUID(id string) bool { return uuid.Validate(id) == nil }
