// This file adds the gate that keeps the draft write barrier's per-verb
// classification from going stale (issue #1827).
//
// internal/toolwrite classifies an action-based tool (manage_asset, s3_object,
// notify, ...) by the value of its verb argument, and any verb it does not
// name as a read is treated as a write. That default is safe, and it goes
// stale silently: when a tool gains a read verb, a draft refuses it as a write
// and no test notices. It happened twice. manage_script command=state was a
// write whatever its state_action said (#1821), and manage_resource gained get
// and list while its rule still read "both its actions write".
//
// This gate reads each action tool's verb enum from the input schema it
// actually advertises and fails on a verb the rule names in neither its reads
// nor its writes, so adding a verb without classifying it fails `make test`.
//
// Run: go test -run TestEveryActionVerbIsClassified .
package structure_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/notifylayer"
	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// Stores the owners below are handed only so that they register their tools.
// Registration checks a store is present and calls none of its methods, so
// each is the interface embedded as nil: a call reaching one would panic,
// which is the right answer for a fixture that must never execute a tool.
type (
	registrationPromptStore  struct{ prompt.Store }
	registrationScriptStore  struct{ script.Store }
	registrationChannelStore struct{ notification.ChannelStore }
)

// TestEveryActionVerbIsClassified fails when an action tool's advertised verb
// enum carries a value internal/toolwrite names in neither the tool's reads nor
// its writes, when a verb argument advertises no enum to check, or when an
// action tool is missing from the assembled listing.
//
// It assembles the owners of every action tool onto one server and reads
// tools/list through a real client, because that listing is what an agent and
// a script author read: a schema checked anywhere else could differ from the
// one served.
func TestEveryActionVerbIsClassified(t *testing.T) {
	schemas := listedInputSchemas(t, actionToolServer(t))

	var problems []string
	for _, tool := range toolwrite.ActionTools() {
		schema, ok := schemas[tool]
		if !ok {
			problems = append(problems, tool+" is not in the assembled listing; add its owner to actionToolServer")
			continue
		}
		problems = append(problems, toolwrite.UnclassifiedVerbs(tool, schemaEnum(schema))...)
	}
	sort.Strings(problems)

	require.Empty(t, problems,
		"action tool verbs the draft write barrier has not classified:\n  %s\nName each verb in its rule's "+
			"reads or writes (actionTools, internal/toolwrite/toolwrite.go), and give a verb argument described "+
			"only in prose an enum in the tool's input schema.", strings.Join(problems, "\n  "))
}

// actionToolServer registers every owner of an action tool on one server with
// the least each needs in order to register.
func actionToolServer(t *testing.T) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "toolwrite-verbs", Version: "v0"}, nil)

	portalkit.New(portalkit.Config{Name: "portal"}).RegisterTools(server)

	mem, err := memory.New("memory", nil, nil)
	require.NoError(t, err)
	mem.RegisterTools(server)

	promptlayer.New(promptlayer.Config{Store: registrationPromptStore{}}).RegisterTool(server)
	scriptlayer.New(scriptlayer.Config{Store: registrationScriptStore{}}).RegisterTool(server)

	notify := notifylayer.New(notifylayer.Config{
		Channels: registrationChannelStore{}, Enqueuer: &notification.Enqueuer{},
	})
	require.NotNil(t, notify, "notify registers only with channels and an enqueuer")
	notify.RegisterTool(server)

	objects, err := s3kit.New("s3", s3kit.Config{
		Region: "us-east-1", Endpoint: "http://127.0.0.1:1", AccessKeyID: "k", SecretAccessKey: "s",
	})
	require.NoError(t, err)
	objects.RegisterTools(server)
	return server
}

// listedInputSchemas connects an in-memory client and returns each listed
// tool's input schema as the client decoded it.
func listedInputSchemas(t *testing.T, server *mcp.Server) map[string]map[string]any {
	t.Helper()
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverSide, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "gate", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientSide, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	out := map[string]map[string]any{}
	for tool, err := range cs.Tools(ctx, nil) {
		require.NoError(t, err)
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(raw, &schema))
		out[tool.Name] = schema
	}
	return out
}

// schemaEnum reads the string enum of one top-level property, reporting
// ok=false when the property is absent or carries no enum.
func schemaEnum(schema map[string]any) func(arg string) ([]string, bool) {
	return func(arg string) ([]string, bool) {
		props, _ := schema["properties"].(map[string]any)
		prop, _ := props[arg].(map[string]any)
		values, ok := prop["enum"].([]any)
		if !ok {
			return nil, false
		}
		out := make([]string, 0, len(values))
		for _, v := range values {
			if s, isString := v.(string); isString {
				out = append(out, s)
			}
		}
		return out, true
	}
}
