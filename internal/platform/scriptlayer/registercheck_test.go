package scriptlayer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// tableChecker is a toolkit that answers the register-target check, the way
// the asset toolkit does.
type tableChecker struct {
	registry.Toolkit
	refuse map[string]error
	asked  []string
}

func (c *tableChecker) CheckTableTarget(_ context.Context, connection string) error {
	c.asked = append(c.asked, connection)
	return c.refuse[connection]
}

type listedToolkits []registry.Toolkit

func (l listedToolkits) All() []registry.Toolkit { return l }

// TestValidate_FlagsARegisterConnectionThatCannotHoldATable is #2213's
// validate half: a literal register= connection the caller cannot register
// on is an error finding, so the mistake shows before a save rather than as
// a run that wrote its output and then failed.
func TestValidate_FlagsARegisterConnectionThatCannotHoldATable(t *testing.T) {
	h, _ := newHandle()
	checker := &tableChecker{refuse: map[string]error{
		"warehouse": errors.New("this connection has no scratch catalog and schema configured. Connections you can register a table on: lake"),
	}}
	h.toolkits = listedToolkits{checker}

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdValidate,
		Source: inMain(`platform.export(name="o", rows=[], format="jsonl", destination="resources", key="s/o.jsonl", register={"connection": "warehouse"})
platform.export(name="p", rows=[], format="jsonl", destination="resources", key="s/p.jsonl", register={"connection": "lake"})`),
	}))
	assert.Equal(t, []string{"lake", "warehouse"}, checker.asked)
	assert.Equal(t, false, fields["ok"])
	findings, _ := fields["findings"].([]any)
	messages := make([]string, 0, len(findings))
	for _, f := range findings {
		m, _ := f.(map[string]any)
		msg, _ := m["message"].(string)
		messages = append(messages, msg)
	}
	assert.Contains(t, messages, `register names connection "warehouse", which cannot hold the table: this connection has no scratch catalog and schema configured. Connections you can register a table on: lake`)
	assert.Equal(t, []any{"lake", "warehouse"}, fields["connections"])
}

// TestValidate_NoCheckerLeavesTheReport keeps a deployment without the asset
// toolkit validating as before.
func TestValidate_NoCheckerLeavesTheReport(t *testing.T) {
	h, _ := newHandle()
	h.toolkits = listedToolkits{}
	assert.Nil(t, h.registerCheck(authorCtx()))
	h.toolkits = nil
	assert.Nil(t, h.registerCheck(authorCtx()))
}
