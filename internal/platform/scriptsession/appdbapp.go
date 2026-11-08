package scriptsession

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// Applications is what a session asks about the applications holding an
// application database's schemas (#2539): the one whose run role a script's
// statements log in as, and the one holding a schema a statement was refused
// on.
type Applications struct {
	// RunAs is the id of the application whose run role the statements of
	// the script scriptID runs on connection log in as: the application it
	// is a part of, when that application holds a schema on connection. ""
	// for none.
	RunAs func(ctx context.Context, scriptID, connection string) string
	// Holder is the id and name of the application holding schema on
	// connection, empty for none.
	Holder func(ctx context.Context, connection, schema string) (id, name string)
}

type scriptKey struct{}

// WithScript marks ctx as a session of the script with id scriptID: a draft,
// which carries no producer, names its script this way. "" leaves ctx as it
// is.
func WithScript(ctx context.Context, scriptID string) context.Context {
	if scriptID == "" {
		return ctx
	}
	return context.WithValue(ctx, scriptKey{}, scriptID)
}

// scriptOf is the script a session runs: the one WithScript named, else the
// script a run or an endpoint request is produced by. "" for none.
func scriptOf(ctx context.Context) string {
	if id, ok := ctx.Value(scriptKey{}).(string); ok && id != "" {
		return id
	}
	if p, ok := producedby.From(ctx); ok && p.Kind == producedby.KindScript {
		return p.ID
	}
	return ""
}

// runAs is the application whose run role the session's statements on
// connection log in as, asked once per connection.
func (c *appdbCaller) runAs(connection string) string {
	c.mu.Lock()
	if app, ok := c.apps[connection]; ok {
		c.mu.Unlock()
		return app
	}
	c.mu.Unlock()
	app := ""
	if c.db.Applications.RunAs != nil {
		if id := scriptOf(c.ctx); id != "" {
			app = c.db.Applications.RunAs(c.ctx, id, connection)
		}
	}
	c.mu.Lock()
	if c.apps == nil {
		c.apps = map[string]string{}
	}
	c.apps[connection] = app
	c.mu.Unlock()
	return app
}

// deniedSchemaPrefix opens PostgreSQL's refusal of a schema the role has no
// USAGE on.
const deniedSchemaPrefix = "permission denied for schema "

// insufficientPrivilege is the SQLSTATE of a privilege refusal.
const insufficientPrivilege = "42501"

// CodeSchemaOfAnotherApplication is a statement refused on a schema another
// application holds (#2539).
const CodeSchemaOfAnotherApplication = "schema_of_another_application"

// reword names the application holding the schema a statement was refused
// on, when it is not the script's own: only an application's own scripts
// read and write its schema, and everything else reaches its data through
// what it exposes. The database refused the statement; this only says why.
func (c *appdbCaller) reword(connection string, err error) error {
	refusal, ok := errors.AsType[*RefusalError](err)
	if !ok || c.db.Applications.Holder == nil || connection == "" {
		return err
	}
	pg, _ := refusal.Envelope["postgres"].(map[string]any)
	state, _ := pg["sqlstate"].(string)
	message, _ := pg["message"].(string)
	schema, found := strings.CutPrefix(message, deniedSchemaPrefix)
	if state != insufficientPrivilege || !found {
		return err
	}
	schema = strings.Trim(schema, `"`)
	id, name := c.db.Applications.Holder(c.ctx, connection, schema)
	if id == "" || id == c.runAs(connection) {
		return err
	}
	text := fmt.Sprintf("schema %s on connection %s belongs to application %q, and only its own scripts read and write it; "+
		"reach its data through what application %q exposes: its endpoints and its exports", schema, connection, name, name)
	env := make(map[string]any, len(refusal.Envelope))
	maps.Copy(env, refusal.Envelope)
	env["code"], env["message"], env["application"] = CodeSchemaOfAnotherApplication, text, name
	return NewRefusal(text, env)
}
