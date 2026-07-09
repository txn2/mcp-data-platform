package platform

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestInitUserStore(t *testing.T) {
	t.Run("nil database disables the directory", func(t *testing.T) {
		p := &Platform{}
		p.initUserStore()
		if p.UserStore() != nil {
			t.Error("expected nil user store without a database")
		}
		if p.users.Directory() != nil {
			t.Error("expected nil directory without a database")
		}
	})

	t.Run("database enables the directory", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		p := &Platform{db: db}
		p.initUserStore()
		if p.UserStore() == nil {
			t.Error("expected a user store when a database is present")
		}
		if p.users.Directory() == nil {
			t.Error("expected a directory when a database is present")
		}
	})
}
