package callhandler

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// The functions each kind's script defines.
const (
	// DecideName is a policy's: decide(call) returns platform.allow(),
	// platform.deny(reason) or platform.require_approval(approvers, summary).
	DecideName = "decide"
	// TransformName is a transform's: transform(call, result) returns the
	// result the caller gets.
	TransformName = "transform"
	// WatchEntry is a watch's: main(), reading the call from run.call.
	WatchEntry = "main"
)

// Contract is the functions a script defines to hold a handler of kind, as a
// refusal names them.
func Contract(kind string) string {
	switch kind {
	case KindPolicy:
		return "decide(call), returning platform.allow(), platform.deny(reason) or platform.require_approval(approvers, summary)"
	case KindTransform:
		return "transform(call, result), returning the result the caller gets"
	}
	return "main(), reading the call it watched from run.call"
}

// CheckPolicy refuses a source a policy could not run.
func CheckPolicy(source string) error { return checkDef(source, KindPolicy, DecideName, 1) }

// CheckTransform refuses a source a transform could not run.
func CheckTransform(source string) error { return checkDef(source, KindTransform, TransformName, 2) }

// CheckWatch refuses a source a watch could not run.
func CheckWatch(source string) error { return checkDef(source, KindWatch, WatchEntry, 0) }

func checkDef(source, kind, name string, params int) error {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return fmt.Errorf("the source does not parse: %w", err)
	}
	if scriptdialect.DefOf(file, name, params) == nil {
		return fmt.Errorf("a %s calls %s, and the source defines no top-level %s taking %d parameter(s)", kind, Contract(kind), name, params)
	}
	return nil
}
