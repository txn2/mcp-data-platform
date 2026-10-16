package srcdir

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoot(t *testing.T) {
	for _, tc := range []struct{ name, id, key, want string }{
		{"asset", "a1", "artifacts/u1/a1/content.csv", "artifacts/u1/a1/"},
		{"asset version", "a1", "artifacts/u1/a1/v3/content.csv", "artifacts/u1/a1/"},
		{"resource revision", "r1", "resources/user/u1/r1/v/x/f.csv", "resources/user/u1/r1/"},
		{"id not in the key", "r2", "resources/user/u1/r1/f.csv", ""},
		{"id only as the file name", "f.csv", "dir/f.csv", ""},
		{"no id", "", "a/b/c.csv", ""},
		{"no directory", "a", "a", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Root(tc.id, tc.key))
		})
	}
}

func TestOwns(t *testing.T) {
	head := "resources/user/u1/r1/v/rev2/f.csv"
	assert.True(t, Owns("b", "r1", head, "s3://b/resources/user/u1/r1/v/rev1/"))
	assert.True(t, Owns("b", "r1", head, "s3://b/resources/user/u1/r1"), "a location written without its slash")
	assert.False(t, Owns("b", "r1", head, "s3://b/resources/user/u1/r10/"), "a prefix of the id")
	assert.False(t, Owns("other", "r1", head, "s3://b/resources/user/u1/r1/"))
	assert.False(t, Owns("b", "r1", head, ""))
	assert.False(t, Owns("b", "", head, "s3://b/x/"))
}

func TestHelpers(t *testing.T) {
	assert.Equal(t, "no external location", Describe(""))
	assert.Equal(t, "s3://b/d/", Describe("s3://b/d/"))
	bucket, key, ok := Split("s3://b/k/x/")
	assert.True(t, ok)
	assert.Equal(t, "b", bucket)
	assert.Equal(t, "k/x/", key)
	_, _, ok = Split("hdfs://x/y")
	assert.False(t, ok)
	_, _, ok = Split("s3:///k")
	assert.False(t, ok)
	assert.Equal(t, []string{"c", "b", "a"}, CandidateIDs("a/b/c/"))
	assert.Equal(t, []string{"a"}, CandidateIDs("a/a/"))
	assert.True(t, Same("s3://b/d/", "s3://b/d"))
	assert.True(t, Under("s3://b/webhooks/esp/raw/", []string{"s3://b/webhooks/esp/"}))
	assert.False(t, Under("s3://b/webhooks/esp2/raw/", []string{"s3://b/webhooks/esp"}))
}
