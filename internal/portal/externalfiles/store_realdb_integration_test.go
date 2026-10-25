//go:build integration

package externalfiles

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// The store reads the directory and the subjects the people signed in as
// (#2290): People lists each signed-in person with the subject their own
// session recorded, and RolesOf finds a person by a user library id that is
// either their address or that subject. A pair a key recorded, and a person
// an administrator pre-added, are not read.
func TestStore_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO users (email, first_name, last_name, source, confirmed, roles) VALUES
			('dana@client-a.example.com', 'Dana', 'Reyes', 'auth', TRUE, '["client-a"]'),
			('lee@client-b.example.com', '', '', 'auth', TRUE, '["client-b"]'),
			('pre@example.com', '', '', 'admin', FALSE, '["client-a"]')`,
		`INSERT INTO identity_subjects (address, subject, from_person) VALUES
			('dana@client-a.example.com', 'sub-dana', TRUE),
			('lee@client-b.example.com', 'key-lee', FALSE)`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	s := NewStore(db)

	people, err := s.People(ctx)
	require.NoError(t, err)
	assert.Equal(t, []Person{
		{Email: "dana@client-a.example.com", FirstName: "Dana", LastName: "Reyes", Roles: []string{"client-a"}, Subject: "sub-dana"},
		{Email: "lee@client-b.example.com", Roles: []string{"client-b"}},
	}, people, "signed-in people only, with the subject their own session recorded")

	for id, want := range map[string][][]string{
		"sub-dana":                  {{"client-a"}},
		"Dana@Client-A.example.com": {{"client-a"}},
		"lee@client-b.example.com":  {{"client-b"}},
		"key-lee":                   {},
		"nobody":                    {},
	} {
		got, err := s.RolesOf(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, want, got, id)
	}
}
