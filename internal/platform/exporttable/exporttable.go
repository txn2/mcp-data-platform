// Package exporttable is the register= argument of a managed script's
// platform.export (#1820): what it may ask for, which outputs it applies to,
// the manage_table call it becomes, and what that call reports back.
//
// It exists so one call can land rows in a file and make the file a table.
// Before it, the path from script rows to a queryable table was three calls a
// script had to get right in order -- export, find the reference, register --
// and it was also the only path free text had into SQL, because trino_execute
// binds no parameters. The registration is not a second mechanism: it is the
// same manage_table register a script could call itself, issued over the run's
// own session, so it is authorized and audited the way that call is.
//
// It knows nothing about Starlark or about the run. The host binding hands it
// plain Go values and the tool's structured result.
package exporttable

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// Tool and Action are the call a registration becomes.
const (
	Tool   = "manage_table"
	Action = "register"
)

// The keys a register= argument, and the table it reports, are written with.
const (
	keyConnection = "connection"
	keyTableName  = "table_name"
	keyFollow     = "follow"
)

// Formats a registration reads. Any other export format is refused before
// anything is written, because the file it would write cannot be a table.
var registrable = map[string]bool{"csv": true, "jsonl": true, "parquet": true}

// Spec is what a register= argument asks for.
type Spec struct {
	// Connection is the Trino connection whose scratch schema the table goes
	// in. Required.
	Connection string
	// TableName is the table's name before the persona prefix. Empty takes a
	// slug of the file's name, as manage_table does.
	TableName string
	// Follow moves the table onto each later version of the file. It defaults
	// to true, as manage_table's does: a script that exports to one key every
	// run wants the table to read the latest run.
	Follow bool
}

// keys are the register= keys, sorted for the refusal that lists them.
var keys = []string{keyConnection, keyFollow, keyTableName}

// Parse reads a register= value, already converted from Starlark to plain Go.
// nil means the argument was not passed, and yields a nil Spec.
func Parse(v any) (*Spec, error) {
	if v == nil {
		return nil, nil //nolint:nilnil // no argument is not an error and asks for nothing
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("register must be a dict such as {\"connection\": \"warehouse\"}, got %T", v)
	}
	spec := &Spec{Follow: true}
	for _, key := range sortedKeys(m) {
		if err := spec.set(key, m[key]); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(spec.Connection) == "" {
		return nil, errors.New("register needs a connection: the Trino connection whose scratch schema " +
			"the table goes in; call list_connections to see the ones you can reach")
	}
	return spec, nil
}

// set applies one register= key.
func (s *Spec) set(key string, value any) error {
	switch key {
	case keyConnection, keyTableName:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("register %s must be a string, got %T", key, value)
		}
		if key == keyConnection {
			s.Connection = strings.TrimSpace(text)
		} else {
			s.TableName = strings.TrimSpace(text)
		}
	case keyFollow:
		follow, ok := value.(bool)
		if !ok {
			return fmt.Errorf("register follow must be True or False, got %T", value)
		}
		s.Follow = follow
	default:
		return fmt.Errorf("register has no key %q; it takes %s", key, strings.Join(keys, ", "))
	}
	return nil
}

// Check refuses a registration the output cannot carry, before the output is
// written: an export whose format no reader registers, and one delivered out
// of the platform, which leaves no stored file for a table to point at.
func (*Spec) Check(format string, stored bool) error {
	if !registrable[format] {
		return fmt.Errorf("register needs format=\"jsonl\", \"parquet\" or \"csv\", the formats a table reads; "+
			"got %q. jsonl and parquet bring every value back exactly and declare each column's type", format)
	}
	if !stored {
		return errors.New("register applies to an output the platform stores, the \"portal\" or \"resources\" " +
			"destination; a bucket destination delivers the file out of the platform, where no table can " +
			"be pointed at it")
	}
	return nil
}

// Args renders the manage_table call that registers the file a reference
// names.
func (s *Spec) Args(reference string) map[string]any {
	args := map[string]any{
		"action":      Action,
		"reference":   reference,
		keyConnection: s.Connection,
		keyFollow:     s.Follow,
	}
	if s.TableName != "" {
		args[keyTableName] = s.TableName
	}
	return args
}

// Reference names the stored file an export wrote, the way manage_table takes
// it: the managed resource's reference for a library output, and the asset's
// for a portal one.
func Reference(resourceRef, assetID string) string {
	if resourceRef != "" {
		return resourceRef
	}
	return knowledgepage.AssetRef(assetID)
}

// Table is the registration an export made, or would have made.
type Table struct {
	Connection     string   `json:"connection"`
	QueryTable     string   `json:"query_table,omitempty"`
	RegistrationID string   `json:"registration_id,omitempty"`
	Columns        []string `json:"columns,omitempty"`
	// ColumnTypes pairs each column with the type the table declares it as
	// (#1833), the same {name, type} entries manage_table, a search hit and a
	// fetched document carry under this key. One key names one shape: a
	// script author who learned it from one surface reads it the same way
	// here.
	ColumnTypes []TableColumn `json:"column_types,omitempty"`
	Format      string        `json:"format,omitempty"`
	Follow      bool          `json:"follow"`
	// Preview marks a registration a draft reports rather than makes: the
	// output was not written, so there was no file to register.
	Preview bool `json:"preview,omitempty"`
}

// Preview is the Table a draft reports for a registration it did not make.
func (s *Spec) Preview() *Table {
	return &Table{Connection: s.Connection, Follow: s.Follow, Preview: true}
}

// FromResult reads the registration out of manage_table's structured result.
func FromResult(out map[string]any) *Table {
	t := &Table{}
	t.Connection, _ = out[keyConnection].(string)
	t.QueryTable, _ = out["query_table"].(string)
	t.RegistrationID, _ = out["registration_id"].(string)
	t.Format, _ = out["format"].(string)
	t.Follow, _ = out[keyFollow].(bool)
	if cols, ok := out["columns"].([]any); ok {
		for _, c := range cols {
			if name, ok := c.(string); ok {
				t.Columns = append(t.Columns, name)
			}
		}
	}
	t.ColumnTypes = columnTypesOf(out["column_types"], t.Columns)
	return t
}

// TableColumn pairs a column with the type the table declares it as.
type TableColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// columnTypesOf reads the declared type of each column, in Columns' order,
// from the column_types manage_table reports. A result carrying none gives
// none.
func columnTypesOf(raw any, columns []string) []TableColumn {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	byName := make(map[string]string, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		typ, _ := m["type"].(string)
		byName[name] = typ
	}
	out := make([]TableColumn, 0, len(columns))
	for _, c := range columns {
		out = append(out, TableColumn{Name: c, Type: byName[c]})
	}
	return out
}

// Map renders a Table as the plain value a script receives.
func (t *Table) Map() map[string]any {
	cols := make([]any, 0, len(t.Columns))
	for _, c := range t.Columns {
		cols = append(cols, c)
	}
	types := make([]any, 0, len(t.ColumnTypes))
	for _, c := range t.ColumnTypes {
		types = append(types, map[string]any{"name": c.Name, "type": c.Type})
	}
	out := map[string]any{
		keyConnection:  t.Connection,
		keyFollow:      t.Follow,
		"preview":      t.Preview,
		"columns":      cols,
		"column_types": types,
	}
	if t.QueryTable != "" {
		out["query_table"] = t.QueryTable
	}
	if t.RegistrationID != "" {
		out["registration_id"] = t.RegistrationID
	}
	if t.Format != "" {
		out["format"] = t.Format
	}
	return out
}

// sortedKeys returns a map's keys in order, so the first bad key reported is
// the same one every time.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
