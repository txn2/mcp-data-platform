package appsvc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const libScript = "33333333-3333-3333-3333-333333333333"

// libraryShop is a released application whose script loads its library at
// the version the release pins.
func libraryShop(t *testing.T) (*Service, *fakeScripts) {
	t.Helper()
	svc, _, scripts, _, _ := fixture()
	scripts.scripts[libScript] = &script.Script{ID: libScript, Name: "helpers", OwnerEmail: jane.Email, Version: 1, Library: true}
	scripts.sources = map[string]string{
		scriptA:   "load(\"lib:helpers@1\", \"fmt\")\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    platform.result(fmt(1))\n",
		libScript: "def fmt(x):\n    \"\"\"Formats.\"\"\"\n    return x\n",
	}
	svc.Diagrams = DiagramDeps{
		Bindings: func(_ context.Context, id string) ([]appdiagram.Binding, error) {
			if id == scriptA {
				return []appdiagram.Binding{{Name: "shop-api", Path: "/"}, {Name: "shop-orders", Path: "/{id}"}}, nil
			}
			return nil, nil
		},
		AssetContent: func(context.Context, string, int) string { return `fetch("/endpoints/x")` },
		SchemaWrites: func(context.Context, string, string) []string { return nil },
	}
	svc.Schedules = fakeSchedules{scriptA: {{CronSpec: "0 6 * * *", Timezone: "UTC"}, {CronSpec: "0 18 * * *", Timezone: "UTC"}}}
	svc.Webhooks = fakeWebhooks{byScript: map[string][]scriptevents.Handler{scriptA: {{Source: "orders-hook", Enabled: true}, {Source: "esp", Enabled: true}}}}
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	for _, p := range [][2]string{{application.KindScript, scriptA}, {application.KindScript, libScript}, {application.KindAsset, "as1"}} {
		_, err = svc.AddPart(ctx, jane, "shop", p[0], p[1])
		require.NoError(t, err)
	}
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	return svc, scripts
}

// TestDiagram_VersionsAndCompare draws the current release, the newest
// versions and a release by number, and compares two.
func TestDiagram_VersionsAndCompare(t *testing.T) {
	svc, scripts := libraryShop(t)
	ctx := context.Background()
	d, err := svc.Diagram(ctx, admin, DiagramRequest{App: "shop", Version: "", Compare: "", View: nil})
	require.NoError(t, err)
	assert.Equal(t, DiagramCurrent, d.Version)
	assert.Empty(t, d.Findings)
	assert.Contains(t, edges(d), "script:"+scriptA+" loads script:"+libScript)
	assert.Contains(t, edges(d), "asset:as1 calls endpoint:x", "an administrator's diagram reads the asset")
	// Every handler of a script is drawn (#2441, #2434).
	card := detailOf(d, "script:"+scriptA)
	for _, want := range []string{
		"schedule 0 6 * * * UTC", "schedule 0 18 * * * UTC", "endpoint /endpoints/shop-api/", "endpoint /endpoints/shop-orders/{id}",
	} {
		assert.Contains(t, card, want)
	}
	assert.Contains(t, edges(d), "source:orders-hook event script:"+scriptA)
	assert.Contains(t, edges(d), "source:esp event script:"+scriptA)

	// A newer library version: latest pins it at 2 while the script loads 1.
	scripts.scripts[libScript].Version = 2
	latest, err := svc.Diagram(ctx, admin, DiagramRequest{App: "shop", Version: DiagramLatest, Compare: "1", View: nil})
	require.NoError(t, err)
	assert.Equal(t, "1", latest.ComparedWith)
	require.Len(t, latest.Findings, 1)
	assert.Equal(t, appdiagram.FindingLibraryVersion, latest.Findings[0].Kind)

	byNumber, err := svc.Diagram(ctx, admin, DiagramRequest{App: "shop", Version: "1", Compare: "", View: nil})
	require.NoError(t, err)
	assert.Empty(t, byNumber.Findings, "release 1 pins the library at the version loaded")
	_, err = svc.Diagram(ctx, admin, DiagramRequest{App: "shop", Version: "nope", Compare: "", View: nil})
	require.ErrorContains(t, err, "current, latest or a release's number")
	_, err = svc.Diagram(ctx, admin, DiagramRequest{App: "shop", Version: "9", Compare: "", View: nil})
	require.ErrorIs(t, err, application.ErrNoRelease)
	_, err = svc.Diagram(ctx, admin, DiagramRequest{App: "shop", Version: "", Compare: "9", View: nil})
	require.ErrorIs(t, err, application.ErrNoRelease)
	_, err = svc.Diagram(ctx, admin, DiagramRequest{App: "none", Version: "", Compare: "", View: nil})
	require.ErrorIs(t, err, application.ErrNotFound)

	other := application.Actor{Email: "x@example.com"}
	hidden, err := svc.Diagram(ctx, other, DiagramRequest{App: "shop", Version: "", Compare: "", View: func(context.Context, *portaldomain.Asset) bool { return false }})
	require.NoError(t, err)
	assert.NotContains(t, edges(hidden), "asset:as1 calls endpoint:x", "an asset the caller cannot open is not read")
}

// TestFindings_InGetAndAtRelease: get reports the findings under problems;
// a library at an unpinned version refuses the release naming the versions,
// and the release succeeds once the load matches; a save reports the
// findings it introduced.
func TestFindings_InGetAndAtRelease(t *testing.T) {
	svc, scripts := libraryShop(t)
	ctx := context.Background()
	scripts.scripts[libScript].Version = 2
	st, err := svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	require.Len(t, st.Findings, 1)
	assert.True(t, st.Findings[0].Blocks)
	assert.Contains(t, st.Problems, st.Findings[0].Message)

	_, err = svc.Release(ctx, admin, "shop", "")
	require.ErrorContains(t, err, "loads library helpers at version 1")
	require.ErrorContains(t, err, `load("lib:helpers@2"`)

	scripts.scripts[scriptA].Version = 2
	scripts.sources[scriptA] = "load(\"lib:helpers@2\", \"fmt\")\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    platform.result(fmt(1))\n"
	rel, err := svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	assert.Empty(t, rel.Changes.Findings)

	// The fake version store answers one source per script: what the save
	// introduced is read against the same source at the old version, so a
	// script with no change introduces nothing.
	assert.Empty(t, svc.FindingsIntroduced(ctx, scriptA, 1))
	assert.Nil(t, svc.FindingsIntroduced(ctx, scriptB, 1), "a script in no application")
	scripts.scripts[scriptA].Version = 3
	scripts.sources[scriptA+"@3"] = "load(\"lib:helpers@1\", \"fmt\")\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    platform.result(fmt(1))\n"
	introduced := svc.FindingsIntroduced(ctx, scriptA, 2)
	require.Len(t, introduced, 1, "loading the library at 1 again is new at version 3")
	assert.Contains(t, introduced[0], "application shop: script shop-api loads library helpers at version 1")
}

func detailOf(d *appdiagram.Diagram, id string) []string {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n.Detail
		}
	}
	return nil
}

func edges(d *appdiagram.Diagram) []string {
	out := make([]string, 0, len(d.Links))
	for _, l := range d.Links {
		out = append(out, l.From+" "+l.Kind+" "+l.To)
	}
	return out
}
