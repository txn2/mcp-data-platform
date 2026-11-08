package sitegw

import (
	"os"
	"testing"
)

// cacheRoot holds each test harness's own disk cache: an unset cache directory
// takes one shared by the whole system, which would carry bytes between tests.
var cacheRoot string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sitegw-test-")
	if err != nil {
		panic(err)
	}
	cacheRoot = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// isolatedCacheDir is a cache directory of the caller's own.
func isolatedCacheDir() string {
	dir, err := os.MkdirTemp(cacheRoot, "")
	if err != nil {
		panic(err)
	}
	return dir
}
