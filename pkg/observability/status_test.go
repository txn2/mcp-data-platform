package observability

import (
	"errors"
	"fmt"
	"testing"
)

type catError struct {
	cat string
	msg string
}

func (e *catError) Error() string         { return e.msg }
func (e *catError) ErrorCategory() string { return e.cat }

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil err is ok", nil, StatusOK},
		{"auth category", &catError{cat: CategoryAuth, msg: "x"}, StatusAuthErr},
		{"authz category", &catError{cat: CategoryAuthz, msg: "x"}, StatusAuthzErr},
		{"declined category", &catError{cat: CategoryDeclined, msg: "x"}, StatusValidationErr},
		{"unknown category falls to internal", &catError{cat: "weird_thing", msg: "x"}, StatusInternalErr},
		{"plain error falls to internal", errors.New("boom"), StatusInternalErr},
		{"wrapped categorized err is recognized", fmt.Errorf("wrap: %w", &catError{cat: CategoryAuthz, msg: "x"}), StatusAuthzErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.err); got != tc.want {
				t.Errorf("ClassifyError = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyToolCallResult(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		isToolError bool
		errCategory string
		want        string
	}{
		{"success", nil, false, "", StatusOK},
		{"tool error no category → upstream", nil, true, "", StatusUpstreamErr},
		{"tool error auth", nil, true, CategoryAuth, StatusAuthErr},
		{"tool error authz", nil, true, CategoryAuthz, StatusAuthzErr},
		{"tool error declined", nil, true, CategoryDeclined, StatusValidationErr},
		{"tool error unknown category → upstream", nil, true, "made_up", StatusUpstreamErr},
		{"protocol err wins over isToolError", errors.New("rpc"), true, "", StatusInternalErr},
		{"protocol err with categorized err", &catError{cat: CategoryAuthz, msg: "x"}, false, "", StatusAuthzErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyToolCallResult(tc.err, tc.isToolError, tc.errCategory); got != tc.want {
				t.Errorf("ClassifyToolCallResult = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHTTPStatusClass(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{0, StatusClassOther},
		{100, StatusClassOther},
		{200, StatusClass2xx},
		{204, StatusClass2xx},
		{299, StatusClass2xx},
		{300, StatusClass3xx},
		{399, StatusClass3xx},
		{400, StatusClass4xx},
		{418, StatusClass4xx},
		{499, StatusClass4xx},
		{500, StatusClass5xx},
		{599, StatusClass5xx},
		{600, StatusClassOther},
		{-1, StatusClassOther},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			if got := HTTPStatusClass(tc.status); got != tc.want {
				t.Errorf("HTTPStatusClass(%d) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestHTTPStatusCategory(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		transport error
		want      string
	}{
		{"2xx ok", 200, nil, StatusOK},
		{"3xx ok", 302, nil, StatusOK},
		{"4xx upstream", 404, nil, StatusUpstreamErr},
		{"5xx upstream", 503, nil, StatusUpstreamErr},
		{"transport error is upstream regardless of status", 200, errors.New("dial"), StatusUpstreamErr},
		{"transport error with 0 status", 0, errors.New("dial"), StatusUpstreamErr},
		{"0 status no transport err falls to upstream", 0, nil, StatusUpstreamErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTTPStatusCategory(tc.status, tc.transport); got != tc.want {
				t.Errorf("HTTPStatusCategory(%d, %v) = %q, want %q", tc.status, tc.transport, got, tc.want)
			}
		})
	}
}
