// Package appdbdrift compares an application database's live schema with the
// schema its applied migrations build (#2406), and writes an existing schema
// as a baseline migration so a database the platform did not create is
// managed from then on.
//
// The expected schema is built, not remembered: the applied migrations are
// applied from zero on a throwaway database (the postgres toolkit's
// DescribeBuilt) and both are read with the same describe, so a difference is
// a change made outside migrations and never a difference in how two
// snapshots were taken.
package appdbdrift

import (
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Difference is one object that differs between the live schema and the one
// its migrations build.
type Difference struct {
	// Kind is the object: table, column, constraint, index, trigger, policy,
	// partition, row_security, view, function, enum or domain.
	Kind string `json:"kind"`
	// Object names it: table, table.column, table.index, function(args).
	Object string `json:"object"`
	// Change is added (in the live schema only), missing (built by the
	// migrations and absent live) or changed.
	Change string `json:"change"`
	// Live and Expected are the two definitions, for a changed object.
	Live     string `json:"live,omitempty"`
	Expected string `json:"expected,omitempty"`
}

// The changes a difference reports.
const (
	ChangeAdded   = "added"
	ChangeMissing = "missing"
	ChangeChanged = "changed"
)

// Compare lists every difference between live and expected, in a stable
// order: tables and their parts, then functions, enums and domains.
func Compare(live, expected *postgres.Description) []Difference {
	c := &comparer{out: []Difference{}}
	compareSet(c, "table", live.Tables, expected.Tables, matcher[postgres.Table]{func(t postgres.Table) string { return t.Name }, c.table})
	compareSet(c, "function", live.Functions, expected.Functions, matcher[postgres.Function]{functionKey, func(l, e postgres.Function) {
		c.changed("function", functionKey(l), functionDef(l), functionDef(e))
	}})
	compareSet(c, "enum", live.Enums, expected.Enums, matcher[postgres.Enum]{func(e postgres.Enum) string { return e.Name }, func(l, e postgres.Enum) {
		c.changed("enum", l.Name, strings.Join(l.Values, ", "), strings.Join(e.Values, ", "))
	}})
	compareSet(c, "domain", live.Domains, expected.Domains, matcher[postgres.Domain]{func(d postgres.Domain) string { return d.Name }, func(l, e postgres.Domain) {
		c.changed("domain", l.Name, domainDef(l), domainDef(e))
	}})
	return c.out
}

// comparer collects differences.
type comparer struct{ out []Difference }

// changed records a difference when the two definitions differ.
func (c *comparer) changed(kind, object, live, expected string) {
	if live != expected {
		c.out = append(c.out, Difference{Kind: kind, Object: object, Change: ChangeChanged, Live: live, Expected: expected})
	}
}

// matcher is how compareSet pairs items: the key that names one, and what to
// do with an item both sides hold.
type matcher[T any] struct {
	key  func(T) string
	both func(l, e T)
}

// compareSet matches two lists by key: a key on one side only is added or
// missing, and a key on both is handed to both.
func compareSet[T any](c *comparer, kind string, live, expected []T, m matcher[T]) {
	key, both := m.key, m.both
	exp := make(map[string]T, len(expected))
	for _, e := range expected {
		exp[key(e)] = e
	}
	seen := map[string]bool{}
	for _, l := range live {
		k := key(l)
		seen[k] = true
		if e, ok := exp[k]; ok {
			both(l, e)
			continue
		}
		c.out = append(c.out, Difference{Kind: kind, Object: k, Change: ChangeAdded})
	}
	missing := []string{}
	for k := range exp {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	slices.Sort(missing)
	for _, k := range missing {
		c.out = append(c.out, Difference{Kind: kind, Object: k, Change: ChangeMissing})
	}
}

// table compares one table's parts.
func (c *comparer) table(l, e postgres.Table) {
	name := l.Name
	c.changed("table", name, l.Kind, e.Kind)
	c.changed("row_security", name, rowSecurity(l), rowSecurity(e))
	c.changed("partition", name+" (key)", l.PartitionKey, e.PartitionKey)
	if l.View != nil || e.View != nil {
		c.changed("view", name, viewDef(l.View), viewDef(e.View))
	}
	prefix := name + "."
	sub := func(kind string, items func(postgres.Table) map[string]string) {
		li, ei := items(l), items(e)
		keys := func(m map[string]string) []string {
			ks := make([]string, 0, len(m))
			for k := range m {
				ks = append(ks, k)
			}
			slices.Sort(ks)
			return ks
		}
		compareSet(c, kind, keys(li), keys(ei), matcher[string]{func(k string) string { return prefix + k }, func(k, _ string) {
			c.changed(kind, prefix+k, li[k], ei[k])
		}})
	}
	sub("column", columnDefs)
	sub("constraint", func(t postgres.Table) map[string]string {
		return byName(t.Constraints, func(x postgres.Constraint) (string, string) { return x.Name, x.Definition })
	})
	sub("index", func(t postgres.Table) map[string]string {
		return byName(t.Indexes, func(x postgres.TableIndex) (string, string) { return x.Name, x.Definition })
	})
	sub("trigger", func(t postgres.Table) map[string]string {
		return byName(t.Triggers, func(x postgres.Trigger) (string, string) { return x.Name, x.Definition + " " + x.Enabled })
	})
	sub("policy", func(t postgres.Table) map[string]string {
		return byName(t.Policies, func(x postgres.Policy) (string, string) { return x.Name, policyDef(x) })
	})
	sub("partition", func(t postgres.Table) map[string]string {
		return byName(t.Partitions, func(x postgres.Partition) (string, string) { return x.Name, x.Bound })
	})
}

// byName maps a table's parts by name to their definitions.
func byName[T any](items []T, f func(T) (string, string)) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		k, v := f(it)
		out[k] = v
	}
	return out
}

// columnDefs writes each column as its type, nullability and default.
func columnDefs(t postgres.Table) map[string]string {
	return byName(t.Columns, func(col postgres.TableColumn) (string, string) {
		def := col.Type
		if !col.Nullable {
			def += " NOT NULL"
		}
		if col.Default != "" {
			def += " DEFAULT " + col.Default
		}
		return col.Name, def
	})
}

func rowSecurity(t postgres.Table) string {
	switch {
	case t.ForceRowSecurity:
		return "enabled, forced"
	case t.RowSecurity:
		return "enabled"
	default:
		return "disabled"
	}
}

func viewDef(v *postgres.View) string {
	if v == nil {
		return ""
	}
	def := v.Definition
	if v.SecurityInvoker {
		def += " [security_invoker]"
	}
	if v.SecurityBarrier {
		def += " [security_barrier]"
	}
	if v.CheckOption != "" {
		def += " [check_option=" + v.CheckOption + "]"
	}
	return def
}

func policyDef(p postgres.Policy) string {
	mode := "PERMISSIVE"
	if !p.Permissive {
		mode = "RESTRICTIVE"
	}
	return mode + " FOR " + p.Command + " TO " + strings.Join(p.Roles, ",") + " USING (" + p.Using + ") WITH CHECK (" + p.WithCheck + ")"
}

func functionKey(f postgres.Function) string { return f.Name + "(" + f.Arguments + ")" }

func functionDef(f postgres.Function) string {
	parts := []string{f.Kind, f.Returns, f.Language, f.Volatility, f.SearchPath, f.Body}
	if f.SecurityDefiner {
		parts = append(parts, "SECURITY DEFINER")
	}
	return strings.Join(parts, " | ")
}

func domainDef(d postgres.Domain) string {
	def := d.BaseType
	if !d.Nullable {
		def += " NOT NULL"
	}
	if d.Default != "" {
		def += " DEFAULT " + d.Default
	}
	return def + " " + strings.Join(d.Checks, " ")
}
