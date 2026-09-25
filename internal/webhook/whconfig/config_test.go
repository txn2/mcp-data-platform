package whconfig

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/webhook/receiver"
)

func TestDefaultsOn(t *testing.T) {
	var c Config
	assert.True(t, c.ReceiverEnabled())
	assert.True(t, c.CompactorEnabled())
	assert.NoError(t, c.Validate())
	assert.Equal(t, receiver.DefaultWriteTimeout, c.WriteTimeout())

	off := false
	c.Receiver.Enabled = &off
	c.Compactor.Enabled = &off
	assert.False(t, c.ReceiverEnabled())
	assert.False(t, c.CompactorEnabled())
}

func TestTuningAndValidate(t *testing.T) {
	c := Config{Compactor: CompactorConfig{Poll: time.Second, Grace: time.Minute, Batch: 2}}
	tn := c.Tuning()
	assert.Equal(t, time.Second, tn.Poll)
	assert.Equal(t, time.Minute, tn.Grace)
	assert.Equal(t, 2, tn.Batch)

	c.Receiver.WriteTimeout = 3 * time.Second
	assert.Equal(t, 3*time.Second, c.WriteTimeout())

	assert.Error(t, Config{Compactor: CompactorConfig{Grace: -1}}.Validate())
	assert.Error(t, Config{Compactor: CompactorConfig{Batch: -1}}.Validate())
}
