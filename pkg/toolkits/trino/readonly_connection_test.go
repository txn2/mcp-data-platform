package trino

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

const writeSQL = "DELETE FROM sales.orders WHERE id = 1"

// interceptOn runs the interceptor as the toolkit does: the middleware's
// Before hook resolves the connection onto the context, then the handler
// intercepts the SQL.
func interceptOn(t *testing.T, i *ReadOnlyInterceptor, conn, sql string) error {
	t.Helper()
	ctx, err := i.Before(context.Background(), trinotools.NewToolContext(
		trinotools.ToolExecute, trinotools.ExecuteInput{SQL: sql, Connection: conn}))
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	_, err = i.Intercept(ctx, sql, trinotools.ToolExecute)
	return err
}

func TestReadOnlyInterceptor_PerConnection(t *testing.T) {
	newInterceptor := func() *ReadOnlyInterceptor {
		return NewConnectionReadOnlyInterceptor("reporting", map[string]bool{
			"reporting": true,
			"etl":       false,
		})
	}

	t.Run("blocks write SQL on the read-only connection", func(t *testing.T) {
		err := interceptOn(t, newInterceptor(), "reporting", writeSQL)
		if err == nil {
			t.Fatal("write SQL was allowed through the read-only connection")
		}
		if !strings.Contains(err.Error(), `"reporting"`) {
			t.Errorf("error should name the refusing connection, got %q", err)
		}
	})

	t.Run("allows write SQL on the write-capable connection", func(t *testing.T) {
		if err := interceptOn(t, newInterceptor(), "etl", writeSQL); err != nil {
			t.Errorf("write SQL blocked on a write-capable connection: %v", err)
		}
	})

	t.Run("allows read SQL on the read-only connection", func(t *testing.T) {
		if err := interceptOn(t, newInterceptor(), "reporting", "SELECT 1"); err != nil {
			t.Errorf("read SQL blocked: %v", err)
		}
	})

	t.Run("an omitted connection resolves to the default", func(t *testing.T) {
		if err := interceptOn(t, newInterceptor(), "", writeSQL); err == nil {
			t.Error("write SQL was allowed when the read-only default should have refused it")
		}
		writeDefault := NewConnectionReadOnlyInterceptor("etl", map[string]bool{
			"reporting": true,
			"etl":       false,
		})
		if err := interceptOn(t, writeDefault, "", writeSQL); err != nil {
			t.Errorf("write SQL blocked with a write-capable default: %v", err)
		}
	})

	t.Run("a call with no resolved connection refuses writes", func(t *testing.T) {
		// Intercept without Before — the state a caller that registered the
		// interceptor without its middleware would leave. The wiring mistake
		// must close the door, not open it, so this holds for a write-capable
		// default too.
		for _, name := range []string{"reporting", "etl"} {
			i := NewConnectionReadOnlyInterceptor(name, map[string]bool{"reporting": true, "etl": false})
			if _, err := i.Intercept(context.Background(), writeSQL, trinotools.ToolExecute); err == nil {
				t.Errorf("default %q: write SQL was allowed with no resolved connection", name)
			}
		}
	})

	t.Run("read SQL is never refused, whatever the connection", func(t *testing.T) {
		// Reads carry no write risk, so an unknown name stays the handler's
		// error to report (multiserver.Config.ClientConfig) rather than
		// becoming a read-only refusal here.
		if err := interceptOn(t, newInterceptor(), "nonesuch", "SELECT 1"); err != nil {
			t.Errorf("read SQL on an unknown connection was refused: %v", err)
		}
		i := newInterceptor()
		if _, err := i.Intercept(context.Background(), "SELECT 1", trinotools.ToolExecute); err != nil {
			t.Errorf("read SQL with no resolved connection was refused: %v", err)
		}
	})

	t.Run("an unconfigured connection refuses write SQL", func(t *testing.T) {
		err := interceptOn(t, newInterceptor(), "nonesuch", writeSQL)
		if err == nil {
			t.Fatal("write SQL was allowed on a connection this toolkit holds no setting for")
		}
		if !strings.Contains(err.Error(), "not a configured Trino connection") {
			t.Errorf("error should say the connection is not configured, got %q", err)
		}
	})
}

func TestReadOnlyInterceptor_Unconditional(t *testing.T) {
	i := NewReadOnlyInterceptor()

	t.Run("blocks write SQL whatever connection is named", func(t *testing.T) {
		for _, conn := range []string{"", "etl", "anything"} {
			err := interceptOn(t, i, conn, writeSQL)
			if err == nil {
				t.Errorf("connection %q: write SQL was allowed", conn)
				continue
			}
			if got := err.Error(); got != "write operations not allowed in read-only mode" {
				t.Errorf("connection %q: error = %q", conn, got)
			}
		}
	})

	t.Run("SetConnection cannot open a hole", func(t *testing.T) {
		i.SetConnection("etl", false)
		if err := interceptOn(t, i, "etl", writeSQL); err == nil {
			t.Error("SetConnection made an unconditional interceptor writable")
		}
	})
}

func TestReadOnlyInterceptor_RuntimeConnections(t *testing.T) {
	i := NewConnectionReadOnlyInterceptor("etl", map[string]bool{"etl": false})

	t.Run("a connection added at runtime is enforced", func(t *testing.T) {
		i.SetConnection("reporting", true)
		if err := interceptOn(t, i, "reporting", writeSQL); err == nil {
			t.Error("write SQL was allowed on a read-only connection added at runtime")
		}
	})

	t.Run("a write-capable connection added at runtime stays writable", func(t *testing.T) {
		i.SetConnection("staging", false)
		if err := interceptOn(t, i, "staging", writeSQL); err != nil {
			t.Errorf("write SQL blocked on a write-capable connection: %v", err)
		}
	})

	t.Run("forgetting a connection leaves nothing writable behind", func(t *testing.T) {
		i.SetConnection("temp", false)
		i.ForgetConnection("temp")
		err := interceptOn(t, i, "temp", writeSQL)
		if err == nil {
			t.Fatal("a removed connection still accepted write SQL")
		}
		if !strings.Contains(err.Error(), "not a configured Trino connection") {
			t.Errorf("error = %q, want the unconfigured-connection refusal", err)
		}
	})
}

// newExecCaller registers the toolkit on a real MCP server, connects an
// in-memory client, and returns a function that calls trino_execute through
// the whole chain. A toolkit registers its tools once, so the server and
// session are shared across the calls of one test.
func newExecCaller(t *testing.T, tk *Toolkit) func(conn, sql string) string {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "trino-test", Version: "v0"}, nil)
	tk.RegisterTools(server)

	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	return func(conn, sql string) string {
		t.Helper()
		args := map[string]any{"sql": sql}
		if conn != "" {
			args["connection"] = conn
		}
		res, callErr := sess.CallTool(ctx, &mcp.CallToolParams{Name: toolExecute, Arguments: args})
		if callErr != nil {
			t.Fatalf("call trino_execute: %v", callErr)
		}
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return sb.String()
	}
}

// unreachable points at a closed loopback port: a call that gets past the
// read-only gate fails at the client instead of hanging.
func unreachableInstance(readOnly bool) Config {
	return Config{
		Host:     "127.0.0.1",
		Port:     1,
		User:     "tester",
		ReadOnly: readOnly,
	}
}

// isReadOnlyRefusal reports whether the tool result is the read-only gate
// refusing the statement, as opposed to any later failure.
func isReadOnlyRefusal(text string) bool {
	return strings.Contains(text, "read-only")
}

// reachedEngine reports whether the statement got past every gate and was sent
// to the Trino client, which fails to dial the closed loopback port. Asserting
// this rather than only the absence of a refusal keeps "not blocked" from
// passing on some earlier, unrelated failure.
func reachedEngine(text string) bool {
	return strings.Contains(text, "connection refused")
}

// TestMultiToolkit_ReadOnlyIsPerConnection is the issue #1269 acceptance test:
// one toolkit, one read-only instance and one write-capable instance, asserted
// in both directions through the assembled MCP server.
func TestMultiToolkit_ReadOnlyIsPerConnection(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "etl",
		Instances: map[string]Config{
			"etl":       unreachableInstance(false),
			"reporting": unreachableInstance(true),
		},
	})
	if err != nil {
		t.Fatalf("NewMulti: %v", err)
	}
	defer func() { _ = tk.Close() }()
	execute := newExecCaller(t, tk)

	t.Run("the read-only connection refuses write SQL", func(t *testing.T) {
		got := execute("reporting", writeSQL)
		if !isReadOnlyRefusal(got) {
			t.Errorf("write SQL was not refused on the read-only connection: %q", got)
		}
		if !strings.Contains(got, "reporting") {
			t.Errorf("refusal does not name the connection: %q", got)
		}
	})

	t.Run("the write-capable connection is not refused", func(t *testing.T) {
		got := execute("etl", writeSQL)
		if isReadOnlyRefusal(got) || !reachedEngine(got) {
			t.Errorf("write SQL did not reach the engine on the write-capable connection: %q", got)
		}
	})

	t.Run("an omitted connection uses the write-capable default", func(t *testing.T) {
		got := execute("", writeSQL)
		if isReadOnlyRefusal(got) || !reachedEngine(got) {
			t.Errorf("write SQL did not reach the engine on the default write-capable connection: %q", got)
		}
	})

	t.Run("the read-only connection still serves read SQL", func(t *testing.T) {
		got := execute("reporting", "SELECT 1")
		if isReadOnlyRefusal(got) || !reachedEngine(got) {
			t.Errorf("read SQL did not reach the engine on the read-only connection: %q", got)
		}
	})
}

// TestMultiToolkit_SingleInstanceReadOnly holds the unchanged single-instance
// behavior: read_only still refuses writes, and its absence still permits
// trino_execute to reach the engine.
func TestMultiToolkit_SingleInstanceReadOnly(t *testing.T) {
	t.Run("read_only refuses write SQL", func(t *testing.T) {
		tk, err := NewMulti(MultiConfig{Instances: map[string]Config{"only": unreachableInstance(true)}})
		if err != nil {
			t.Fatalf("NewMulti: %v", err)
		}
		defer func() { _ = tk.Close() }()
		if got := newExecCaller(t, tk)("", writeSQL); !isReadOnlyRefusal(got) {
			t.Errorf("write SQL was allowed on a read-only single instance: %q", got)
		}
	})

	t.Run("without read_only write SQL is permitted", func(t *testing.T) {
		tk, err := NewMulti(MultiConfig{Instances: map[string]Config{"only": unreachableInstance(false)}})
		if err != nil {
			t.Fatalf("NewMulti: %v", err)
		}
		defer func() { _ = tk.Close() }()
		if got := newExecCaller(t, tk)("", writeSQL); isReadOnlyRefusal(got) || !reachedEngine(got) {
			t.Errorf("write SQL did not reach the engine without read_only: %q", got)
		}
	})
}

// TestSingleToolkit_ReadOnlyIgnoresConnectionArgument covers the one-client
// toolkit, where the connection argument selects nothing: read_only holds for
// every call regardless of what the caller names.
func TestSingleToolkit_ReadOnlyIgnoresConnectionArgument(t *testing.T) {
	tk, err := New("only", unreachableInstance(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = tk.Close() }()

	execute := newExecCaller(t, tk)
	for _, conn := range []string{"", "only", "somewhere-else"} {
		if got := execute(conn, writeSQL); !isReadOnlyRefusal(got) {
			t.Errorf("connection %q: write SQL was allowed: %q", conn, got)
		}
	}
}

// TestToolkit_AddConnectionCarriesReadOnly proves a connection added at
// runtime (admin hot-reload) is enforced from its first call.
func TestToolkit_AddConnectionCarriesReadOnly(t *testing.T) {
	tk, err := NewMulti(MultiConfig{Instances: map[string]Config{"etl": unreachableInstance(false)}})
	if err != nil {
		t.Fatalf("NewMulti: %v", err)
	}
	defer func() { _ = tk.Close() }()

	if addErr := tk.AddConnection("reporting", map[string]any{
		"host":      "127.0.0.1",
		"port":      1,
		"user":      "tester",
		"read_only": true,
	}); addErr != nil {
		t.Fatalf("AddConnection: %v", addErr)
	}

	execute := newExecCaller(t, tk)
	if got := execute("reporting", writeSQL); !isReadOnlyRefusal(got) {
		t.Errorf("write SQL was allowed on a read-only connection added at runtime: %q", got)
	}
	if got := execute("etl", writeSQL); isReadOnlyRefusal(got) || !reachedEngine(got) {
		t.Errorf("write SQL did not reach the engine on the write-capable connection: %q", got)
	}

	if remErr := tk.RemoveConnection("reporting"); remErr != nil {
		t.Fatalf("RemoveConnection: %v", remErr)
	}
	if tk.readOnly == nil {
		t.Fatal("multi-connection toolkit has no read-only interceptor")
	}
	if _, still := tk.readOnly.readOnly["reporting"]; still {
		t.Error("a removed connection kept its read-only setting")
	}
}

// TestToolkit_RejectedAddLeavesSettingsIntact covers the add the manager
// refuses: the default connection cannot be replaced through AddConnection, and
// a rejected call must not carry its config's read_only onto the connection it
// named.
func TestToolkit_RejectedAddLeavesSettingsIntact(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "reporting",
		Instances:         map[string]Config{"reporting": unreachableInstance(true)},
	})
	if err != nil {
		t.Fatalf("NewMulti: %v", err)
	}
	defer func() { _ = tk.Close() }()

	if addErr := tk.AddConnection("reporting", map[string]any{
		"host":      "127.0.0.1",
		"port":      1,
		"user":      "tester",
		"read_only": false,
	}); addErr == nil {
		t.Fatal("AddConnection replaced the default connection; expected a refusal")
	}

	if got := newExecCaller(t, tk)("", writeSQL); !isReadOnlyRefusal(got) {
		t.Errorf("the read-only default became writable through a rejected add: %q", got)
	}
}

// TestToolkit_ConcurrentConnectionChurn runs connection edits against reads of
// the same maps, which is what an admin save does while sessions are live.
// Under -race this fails if either map is mutated unguarded.
func TestToolkit_ConcurrentConnectionChurn(t *testing.T) {
	tk, err := NewMulti(MultiConfig{Instances: map[string]Config{"etl": unreachableInstance(false)}})
	if err != nil {
		t.Fatalf("NewMulti: %v", err)
	}
	defer func() { _ = tk.Close() }()

	const rounds = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range rounds {
			name := fmt.Sprintf("conn-%d", i)
			if addErr := tk.AddConnection(name, map[string]any{
				"host":        "127.0.0.1",
				"port":        1,
				"user":        "tester",
				"read_only":   true,
				"description": name,
			}); addErr != nil {
				t.Errorf("AddConnection %s: %v", name, addErr)
				return
			}
			if remErr := tk.RemoveConnection(name); remErr != nil {
				t.Errorf("RemoveConnection %s: %v", name, remErr)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range rounds {
			tk.ListConnections()
			_, _ = tk.readOnly.Intercept(
				context.WithValue(context.Background(), connectionContextKey{}, "etl"),
				writeSQL, trinotools.ToolExecute)
		}
	}()
	wg.Wait()
}
