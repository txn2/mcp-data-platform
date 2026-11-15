package sitetool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitetraffic"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// asAdmin is the platform context the tool-call middleware sets, put on every
// request a test server receives.
func asAdmin(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		ctx = middleware.WithPlatformContext(ctx, &middleware.PlatformContext{UserEmail: adminEmail, IsAdmin: true, PersonaName: "analyst"})
		return next(ctx, method, req)
	}
}

// clientAnswering connects a client to server whose person answers every
// question with action, and returns the questions it was asked.
func clientAnswering(t *testing.T, server *mcp.Server, action string) (cs *mcp.ClientSession, asked *[]string) {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	asked = &[]string{}
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			*asked = append(*asked, req.Params.Message)
			return &mcp.ElicitResult{Action: action}, nil
		},
	})
	cs, err = client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs, asked
}

// An agent's promotion to an environment people use asks the person through
// their own client (#2643), end to end: the question names the move and the
// address to look at, an accepted answer carries it out, a declined one
// changes nothing, and a client that cannot ask files the request.
func TestPromote_ThePersonIsAskedThroughTheirClient(t *testing.T) {
	for _, tc := range []struct {
		action   string
		promoted bool
	}{
		{"accept", true},
		{"decline", false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			f := newFixture()
			createSite(t, f)
			publishTo(t, f, site.EnvDev)
			server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
			f.tool.Register(server)
			server.AddReceivingMiddleware(asAdmin)
			cs, asked := clientAnswering(t, server, tc.action)

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolName, Arguments: map[string]any{"command": CmdPromote, "site": siteName, "to": "prod"}})
			require.NoError(t, err)
			require.Len(t, *asked, 1, "the person is asked once")
			assert.Contains(t, (*asked)[0], "prod")
			assert.Contains(t, (*asked)[0], "https://marketing--dev.pages.example.com", "the question gives the address to look at")
			assert.Equal(t, !tc.promoted, res.IsError, text(t, res))
			want := 0
			if tc.promoted {
				want = 1
			}
			assert.Equal(t, want, stored(f).Environment("prod").Release)
			assert.Zero(t, f.approvals.submitted, "a person who was asked has nothing filed for them")
		})
	}

	t.Run("a client that cannot ask", func(t *testing.T) {
		f := newFixture()
		createSite(t, f)
		publishTo(t, f, site.EnvDev)
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
		f.tool.Register(server)
		server.AddReceivingMiddleware(asAdmin)
		res, err := connect(t, server).CallTool(context.Background(), &mcp.CallToolParams{Name: ToolName, Arguments: map[string]any{"command": CmdPromote, "site": siteName, "to": "prod"}})
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Contains(t, text(t, res), "APPROVAL_REQUIRED")
		assert.Equal(t, 1, f.approvals.submitted)
		assert.Zero(t, stored(f).Environment("prod").Release)
	})
}

// addStage adds stage between dev and prod, as an administrator does.
func addStage(t *testing.T, f *fixture) {
	t.Helper()
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "stage", After: site.EnvDev})
}

// #2631, #2632 and #2630 through manage_site: a status alone, a status given
// with a setting, rename_to alone, and remove_environment.
func TestEnvironment_StatusRenameAndRemove(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	addStage(t, f)

	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "stage", Status: site.StatusOffline, Message: "Back soon", BackAt: "2026-11-14T14:00:00Z"})
	stage := stored(f).Environment("stage")
	assert.Equal(t, site.StatusOffline, stage.Status)
	require.NotNil(t, stage.BackAt)
	assert.Equal(t, 14, stage.BackAt.UTC().Hour())

	msg := refusal(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "stage", Status: site.StatusOffline, BackAt: "2pm"})
	assert.Contains(t, msg, "RFC 3339")

	yes := true
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "stage", InternalView: &yes, Status: site.StatusOnline})
	assert.Equal(t, site.StatusOnline, stored(f).Environment("stage").Status)
	assert.True(t, stored(f).Environment("stage").InternalView, "a setting given with a status is applied first")

	msg = refusal(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "stage", RenameTo: "qa", Status: site.StatusOffline})
	assert.Contains(t, msg, "rename_to renames the environment and nothing else")
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "stage", RenameTo: "qa"})
	require.NotNil(t, stored(f).Environment("qa"))

	got := call(t, f, admin(), Input{Command: CmdRemoveEnvironment, Site: siteName, Environment: "qa"})
	assert.Equal(t, "qa", got["removed"])
	assert.Nil(t, stored(f).Environment("qa"))
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdRemoveEnvironment, Site: siteName, Environment: site.EnvDev}), "dev")
}

type fakeTraffic struct {
	by  map[string]sitetraffic.Counts
	err error
}

func (f fakeTraffic) ByEnvironment(context.Context, string, string, string, time.Time) (by map[string]sitetraffic.Counts, order []string, err error) {
	return f.by, nil, f.err
}

// get reports a site's last day of traffic (#2647), and leaves it out when
// the read fails rather than failing the read of the site.
func TestGet_ReportsTheLastDaysTraffic(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	var prod sitetraffic.Counts
	prod.Classes[0] = 40
	prod.Buckets[3] = 40
	f.tool.Traffic = fakeTraffic{by: map[string]sitetraffic.Counts{"prod": prod}}
	got := call(t, f, admin(), Input{Command: CmdGet, Site: siteName})
	traffic, ok := got["traffic"].(map[string]any)
	require.True(t, ok, "get carries traffic: %v", got)
	assert.Equal(t, "24h", traffic["range"])
	total, ok := traffic["total"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 40, total["requests"], 0)
	assert.Contains(t, traffic["environments"], "prod")

	f.tool.Traffic = fakeTraffic{err: errors.New("down")}
	assert.NotContains(t, call(t, f, admin(), Input{Command: CmdGet, Site: siteName}), "traffic")
}
