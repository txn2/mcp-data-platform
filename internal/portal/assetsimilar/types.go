package assetsimilar

import "time"

// What a closeness query answers with: a near asset, a group and its members.

// Match is one asset close to the vector asked about.
type Match struct {
	AssetID    string  `json:"asset_id"`
	Name       string  `json:"name"`
	OwnerEmail string  `json:"owner_email"`
	Similarity float64 `json:"similarity"`
}

// Member is one asset in a group.
type Member struct {
	AssetID        string    `json:"asset_id"`
	Name           string    `json:"name"`
	OwnerID        string    `json:"owner_id,omitempty"`
	OwnerEmail     string    `json:"owner_email"`
	CurrentVersion int       `json:"current_version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Group is a set of one owner's assets each within NearDuplicate of another
// in the set. Similarity is the closest pair's.
type Group struct {
	Members    []Member `json:"members"`
	Similarity float64  `json:"similarity"`
}
