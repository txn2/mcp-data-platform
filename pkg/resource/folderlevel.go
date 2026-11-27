package resource

import (
	"context"
	"database/sql"
	"fmt"
)

// folderLevelStore reads one level of a library's folders (#2837): the
// folders directly inside a parent, each with the files beneath it at every
// depth, rather than every folder of the library. A library of 50,000 folders
// took 1.75 s to read whole and answered with about 6 MB, re-read after every
// write; one level of it takes tens of milliseconds.
type folderLevelStore interface {
	FolderLevel(ctx context.Context, filter Filter, parent string) ([]Folder, error)
}

// buildFolderLevel renders the statement reading the folders directly inside
// parent ("" is the library's root): for each, the files beneath it at every
// depth, the latest change, and how many folders are directly inside it. The
// rows whose child is empty are the parent's own files and stored row.
func buildFolderLevel(filter Filter, parent string) (query string, args []any) {
	filter.Path, filter.Direct = parent, false
	where, args := buildScopeWhere(filter)
	stored, storedArgs, next := storedFolderVisibility(filter, len(args)+1)
	args = append(args, storedArgs...)
	rest := "path"
	if parent != "" {
		// #nosec G202 -- the bound pattern is built here from parent, and the
		// position is an int.
		stored += fmt.Sprintf(" AND (path = $%d OR path LIKE $%d)", next, next+1)
		args = append(args, parent, likePrefix(parent)+"/%")
		rest = fmt.Sprintf("substr(path, %d)", len(parent)+2) //nolint:mnd // past the parent and its slash
	}
	// #nosec G202 -- the only interpolations are the scope predicates, whose
	// values are bound as parameters, and an int position.
	//
	// The rows are grouped twice, by child and grandchild and then by child,
	// so the subfolders are counted by hashing rather than by a sort for a
	// COUNT(DISTINCT): on 150,000 rows the sort spilled to disk and took twice
	// as long.
	query = `
		SELECT child, SUM(n) AS count, MAX(changed) AS updated_at,
		       COUNT(*) FILTER (WHERE grand <> '') AS subfolders
		  FROM (
			SELECT split_part(rest, '/', 1) AS child, split_part(rest, '/', 2) AS grand,
			       SUM(n) AS n, MAX(changed) AS changed
			  FROM (
				SELECT ` + rest + ` AS rest, 1 AS n, updated_at AS changed FROM resources WHERE ` + where + `
				UNION ALL
				SELECT ` + rest + `, 0, created_at FROM resource_folders WHERE ` + stored + `
			  ) AS held
			 GROUP BY 1, 2
		  ) AS level
		 GROUP BY child
		 ORDER BY child`
	return query, args
}

// FolderLevel returns the folders directly inside parent, and, when parent is
// a folder, the parent itself first with its own totals, so a view standing
// in it reads its count from the same answer.
func (s *postgresStore) FolderLevel(ctx context.Context, filter Filter, parent string) ([]Folder, error) { //nolint:revive // interface impl
	if len(filter.Scopes) == 0 && !filter.AllScopes {
		return nil, nil
	}
	query, args := buildFolderLevel(filter, parent)
	// #nosec G701 -- the statement is assembled from constants, bound scope
	// predicates and an int position; parent reaches it only as a parameter.
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing a folder level: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var lv levelBuilder
	lv.parent = parent
	for rows.Next() {
		var (
			child      string
			count, sub int
			changed    sql.NullTime
		)
		if err := rows.Scan(&child, &count, &changed, &sub); err != nil {
			return nil, fmt.Errorf("scanning folder level row: %w", err)
		}
		lv.add(child, count, changed, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating folder level rows: %w", err)
	}
	return lv.folders(), nil
}

// levelBuilder gathers a level's rows into folders.
type levelBuilder struct {
	parent   string
	self     Folder
	selfSeen bool
	children []Folder
}

func (lv *levelBuilder) add(child string, count int, changed sql.NullTime, sub int) {
	lv.self.Count += count
	if changed.Valid && (lv.self.UpdatedAt == nil || changed.Time.After(*lv.self.UpdatedAt)) {
		t := changed.Time
		lv.self.UpdatedAt = &t
	}
	if child == "" {
		lv.selfSeen = true
		return
	}
	f := Folder{Path: joinFolder(lv.parent, child), Count: count, Subfolders: &sub}
	if changed.Valid {
		t := changed.Time
		f.UpdatedAt = &t
	}
	lv.children = append(lv.children, f)
}

func (lv *levelBuilder) folders() []Folder {
	if lv.parent == "" || (!lv.selfSeen && len(lv.children) == 0) {
		return lv.children
	}
	n := len(lv.children)
	lv.self.Path, lv.self.Subfolders = lv.parent, &n
	return append([]Folder{lv.self}, lv.children...)
}

// treeLevel derives one level from a whole tree, for a store that reads no
// level of its own: the folders directly inside parent, the parent first when
// it is listed.
func treeLevel(folders []Folder, parent string) []Folder {
	subs := map[string]int{}
	for _, f := range folders {
		if p := parentFolder(f.Path); p != f.Path {
			subs[p]++
		}
	}
	out := []Folder{}
	for _, f := range folders {
		if f.Path == parent || (parentFolder(f.Path) == parent && f.Path != "") {
			n := subs[f.Path]
			f.Subfolders = &n
			if f.Path == parent {
				out = append([]Folder{f}, out...)
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

func joinFolder(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "/" + child
}
