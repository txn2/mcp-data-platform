package site

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMount(t *testing.T) {
	got, err := ValidateMount(Mount{Prefix: "media", Scope: "global", Folder: "/sites/m/media/"})
	require.NoError(t, err)
	assert.Equal(t, "/media/", got.Prefix, "a prefix is stored in its one form")
	assert.Equal(t, "sites/m/media", got.Folder)

	got, err = ValidateMount(Mount{Prefix: "/img/large/", Scope: "persona", ScopeID: " web ", Folder: "a"})
	require.NoError(t, err)
	assert.Equal(t, "web", got.ScopeID)

	for name, bad := range map[string]Mount{
		"the root":          {Prefix: "/", Scope: "global", Folder: "a"},
		"a parent":          {Prefix: "/a/../b/", Scope: "global", Folder: "a"},
		"a query":           {Prefix: "/a?x/", Scope: "global", Folder: "a"},
		"a reserved path":   {Prefix: "/_viewer/", Scope: "global", Folder: "a"},
		"well-known":        {Prefix: "/.well-known/", Scope: "global", Folder: "a"},
		"a dotted segment":  {Prefix: "/.git/", Scope: "global", Folder: "a"},
		"no folder":         {Prefix: "/m/", Scope: "global"},
		"a folder outside":  {Prefix: "/m/", Scope: "global", Folder: "../x"},
		"an unknown scope":  {Prefix: "/m/", Scope: "team", Folder: "a"},
		"global with an id": {Prefix: "/m/", Scope: "global", ScopeID: "x", Folder: "a"},
		"user without one":  {Prefix: "/m/", Scope: "user", Folder: "a"},
		"an overlong path":  {Prefix: "/" + strings.Repeat("a", 600) + "/", Scope: "global", Folder: "a"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateMount(bad)
			require.ErrorContains(t, err, "media")
		})
	}
}

func TestValidateMounts(t *testing.T) {
	_, err := ValidateMounts([]Mount{{Prefix: "/m/", Scope: "global", Folder: "a"}, {Prefix: "m", Scope: "global", Folder: "b"}})
	require.ErrorContains(t, err, "two folders at /m/")
	many := make([]Mount, MaxMounts+1)
	_, err = ValidateMounts(many)
	require.ErrorContains(t, err, "at most")
	_, err = ValidateMounts([]Mount{{Prefix: "/", Scope: "global", Folder: "a"}})
	require.Error(t, err)
	got, err := ValidateMounts(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMountFor_TheLongestPrefixAndThePathUnderIt(t *testing.T) {
	mounts := []Mount{{Prefix: "/media/"}, {Prefix: "/media/video/"}}
	m, rel, ok := MountFor(mounts, "/media/video/a/b.mp4")
	require.True(t, ok)
	assert.Equal(t, "/media/video/", m.Prefix)
	assert.Equal(t, "a/b.mp4", rel)
	m, rel, ok = MountFor(mounts, "/media/logo.svg")
	require.True(t, ok)
	assert.Equal(t, "/media/", m.Prefix)
	assert.Equal(t, "logo.svg", rel)
	for _, p := range []string{"/media/", "/media/sub/", "/other.png", "/mediax/a"} {
		_, _, ok := MountFor(mounts, p)
		assert.False(t, ok, p)
	}
}
