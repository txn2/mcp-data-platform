// Package grantsource is where a caller's write grants on Global folders are
// read from (#2684): the source the platform installs once at startup, and
// the SQL form of the path-segment rule a grant covers its folder by. It is a
// package of its own so pkg/resource's public surface carries neither.
package grantsource

import (
	"context"
	"fmt"
	"sync"
)

// Grantee is who a grant lookup is for: the person by subject and address,
// and every persona their roles reach.
type Grantee struct {
	Subject string
	Email   string
	// Personas is every persona the person's roles reach, the membership a
	// persona library is read by: a grant to any of them covers the person.
	Personas []string
}

// Source answers the Global folders a grantee holds a write grant on.
type Source interface {
	WritableFolders(ctx context.Context, g Grantee) ([]string, error)
}

var (
	mu sync.RWMutex
	//nolint:gochecknoglobals // installed once at startup, the way the outbound metrics are.
	installed Source
)

// Install sets the source grants are read from; nil uninstalls it.
func Install(src Source) {
	mu.Lock()
	defer mu.Unlock()
	installed = src
}

// Installed is the source in force, nil for none: nobody holds a grant.
func Installed() Source {
	mu.RLock()
	defer mu.RUnlock()
	return installed
}

// UnderSQL is resource.PathUnder as a SQL predicate over two text
// expressions: path is the column or parameter tested, folder the one it must
// lie beneath, by path segment. The grants, the folder moves that rewrite
// them and a site's media mounts all read it, so the Go check and the SQL
// agree.
func UnderSQL(path, folder string) string {
	return fmt.Sprintf("(%[1]s = %[2]s OR left(%[1]s, length(%[2]s) + 1) = %[2]s || '/')", path, folder)
}
