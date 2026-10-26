package webhookwire

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/internal/webhook/receiver"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// noSources is a source store holding none.
type noSources struct{}

func (noSources) List(context.Context) ([]whsource.Source, error) { return nil, nil }
func (noSources) Get(context.Context, string) (whsource.Source, error) {
	return whsource.Source{}, whsource.ErrNotFound
}
func (noSources) Create(context.Context, whsource.Source) error { return nil }
func (noSources) Update(context.Context, whsource.Source) (time.Time, error) {
	return time.Time{}, nil
}
func (noSources) Delete(context.Context, string) error { return nil }

type formRunner struct{ c *epcollect.Collector }

func (f *formRunner) SetCollector(c *epcollect.Collector) { f.c = c }

type formCommands struct{ f epcmd.FormSources }

func (f *formCommands) SetForms(s epcmd.FormSources) { f.f = s }

// #2178: forms store through this replica's receiver, endpoint_set makes a
// form's source through the source service, and platform.decrypt is answered.
func TestAttachForms(t *testing.T) {
	w := &Webhooks{Service: whadmin.New(whadmin.Deps{Sources: noSources{}}), receiver: receiver.New(receiver.Config{})}
	run, cmds, tokens := &formRunner{}, &formCommands{}, &scripttoken.Issuer{}
	w.AttachForms(FormDeps{Runner: run, Commands: cmds, Tokens: tokens})
	require.NotNil(t, run.c)
	assert.Same(t, w.receiver, run.c.Writer)
	require.NotNil(t, cmds.f)

	_, err := tokens.Bind(scripttoken.Binding{ScriptID: "s"}, nil).CallTool(context.Background(), scripttoken.CapabilityDecrypt,
		map[string]any{"form": "nope", "payload": map[string]any{}})
	require.ErrorContains(t, err, `no form named "nope"`, "the decrypter is attached")
}

// A replica without the receiver refuses each submission rather than
// dropping it.
func TestAttachForms_NoReceiver(t *testing.T) {
	w := &Webhooks{Service: whadmin.New(whadmin.Deps{})}
	run := &formRunner{}
	w.AttachForms(FormDeps{Runner: run, Commands: &formCommands{}, Tokens: &scripttoken.Issuer{}})
	require.NotNil(t, run.c)
	err := run.c.Store(context.Background(), epcollect.Submission{ScriptID: "s"})
	require.ErrorIs(t, err, errNoReceiver)
	src, ok := noReceiver{}.FormSource(context.Background(), "s")
	assert.True(t, ok)
	assert.True(t, src.IsForm())
	require.ErrorIs(t, noReceiver{}.Deliver(context.Background(), whsource.Source{}, whevent.Event{}, nil), errNoReceiver)
}

func TestAttachForms_NoWebhooks(t *testing.T) {
	var none *Webhooks
	run := &formRunner{}
	none.AttachForms(FormDeps{Runner: run})
	assert.Nil(t, run.c, "a deployment without webhooks stores nothing, as before")
}
