package portalstore

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// The script runner is assembled before the table registrar exists, so its
// follower reaches the registrar through the toolkit this handle owns, which
// the composition root binds later (#1536). A handle with no toolkit, or a
// toolkit never bound, reports nothing.

type stubTableRegistrar struct {
	asked []string
}

func (*stubTableRegistrar) Register(context.Context, string, string, string, portalkit.RegisterOptions) (*portalkit.TableRegistration, error) {
	return nil, errors.New("not reached")
}
func (*stubTableRegistrar) Unregister(context.Context, string) error { return nil }
func (*stubTableRegistrar) Tables(context.Context, string) ([]portalkit.TableRegistration, error) {
	return nil, nil
}
func (*stubTableRegistrar) DropAssetTables(context.Context, string) {}
func (s *stubTableRegistrar) FollowAssetTables(_ context.Context, assetID string, version int) []string {
	s.asked = append(s.asked, assetID+"@"+strconv.Itoa(version))
	return []string{"scratch.uploads.t on scratch now reads version " + strconv.Itoa(version) + "."}
}
func (*stubTableRegistrar) FollowResourceTables(context.Context, string, int) []string { return nil }

func TestFollowAssetTables_ReachesTheBoundRegistrar(t *testing.T) {
	h := NewFromStores(Stores{}, nil, Config{Name: "portal"})
	assert.Nil(t, h.FollowAssetTables(context.Background(), "a1", 2), "nothing bound yet")

	reg := &stubTableRegistrar{}
	h.Toolkit().SetTableRegistrar(reg)
	assert.Equal(t, []string{"scratch.uploads.t on scratch now reads version 2."},
		h.FollowAssetTables(context.Background(), "a1", 2))
	assert.Equal(t, []string{"a1@2"}, reg.asked)
}

func TestFollowAssetTables_NilHandle(t *testing.T) {
	var h *Handle
	assert.Nil(t, h.FollowAssetTables(context.Background(), "a1", 2))
}
