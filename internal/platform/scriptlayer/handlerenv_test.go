package scriptlayer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer/handlercmd"
)

// handler_environment carries the handler and environment to the handler
// service and answers its refusal (#2696).
func TestHandlerEnvironmentCommand(t *testing.T) {
	h, _ := handlerHandle(t, "default")
	res := call(t, h, adminCtx(), manageScriptInput{Command: handlercmd.Environment, Handler: "nope", Environment: "dev"})
	assert.True(t, res.IsError)
	none := &Handle{}
	res = call(t, none, adminCtx(), manageScriptInput{Command: handlercmd.Environment, Handler: "nope", Environment: "dev"})
	assert.True(t, res.IsError)
}
