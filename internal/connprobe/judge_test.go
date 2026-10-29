package connprobe_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// TestJudgeGET holds #2368's reading of a probing GET: a 401 anywhere and a
// 403 on a declared operation reject the credential; a 403 on an undeclared
// route says only that the credential cannot reach it; a sign-in that returned
// a session is named as the credential working.
func TestJudgeGET(t *testing.T) {
	for _, tc := range []struct {
		name string
		get  connprobe.GET
		ok   bool
		want []string
	}{
		{
			"401 at the base",
			connprobe.GET{Base: "https://u", Path: "/", Status: http.StatusUnauthorized},
			false,
			[]string{"answered GET / with HTTP 401", "rejecting this connection's credential rather than the route"},
		},
		{
			"403 at an undeclared base",
			connprobe.GET{Base: "https://u", Path: "/", Status: http.StatusForbidden},
			true,
			[]string{"the credential cannot reach /, which the connection's catalog does not declare"},
		},
		{
			"403 on a declared read",
			connprobe.GET{Base: "https://u", Path: "/d", Declared: true, Status: http.StatusForbidden},
			false,
			[]string{"rejecting this connection's credential on an operation its catalog declares"},
		},
		{
			"200 on a declared read",
			connprobe.GET{Base: "https://u", Path: "/d", Declared: true, Status: http.StatusOK},
			true,
			[]string{"the operation is one the connection's catalog declares"},
		},
		{
			"404 at the base",
			connprobe.GET{Base: "https://u", Path: "/", Status: http.StatusNotFound},
			true,
			[]string{"any answer proves the route and the credential"},
		},
		{
			"signed in, 403 at the base",
			connprobe.GET{Prefix: "grant; ", Base: "https://u", Path: "/", SignedIn: true, Status: http.StatusForbidden},
			true,
			[]string{"grant; the sign-in returned a session, so the credential works; ", "cannot reach /"},
		},
		{
			"signed in, 401",
			connprobe.GET{Base: "https://u", Path: "/", SignedIn: true, Status: http.StatusUnauthorized},
			false,
			[]string{"rejecting the session the sign-in returned"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := connprobe.JudgeGET(tc.get)
			if res.OK != tc.ok || res.Error != "" {
				t.Errorf("%+v, want ok=%v and no error", res, tc.ok)
			}
			for _, w := range tc.want {
				if !strings.Contains(res.Detail, w) {
					t.Errorf("detail %q lacks %q", res.Detail, w)
				}
			}
		})
	}
}
