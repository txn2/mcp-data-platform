package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2332: an event run reads the event it was started for as run.event; every
// other run reads None.
func TestRun_EventIsReadFromTheRunRecord(t *testing.T) {
	source := `
if run.event == None:
    print("no event")
else:
    print(run.event["id"], run.event["source"], run.event["payload"]["values"]["email"])
`
	result, err := Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "run_1", FireTime: fireTime, Caller: &recordingCaller{},
		Event: map[string]any{
			"id": "ev1", "source": "form-contact", "type": "form_submission",
			"payload": map[string]any{"values": map[string]any{"email": "ana@example.com"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "ev1 form-contact ana@example.com\n", result.Log)

	result, err = Run(context.Background(), Options{Source: source, Name: "test", RunID: "run_2", FireTime: fireTime, Caller: &recordingCaller{}})
	require.NoError(t, err)
	assert.Equal(t, "no event\n", result.Log)

	_, err = Run(context.Background(), Options{
		Source: `run.event["id"] = "x"`, Name: "test", RunID: "run_3", FireTime: fireTime, Caller: &recordingCaller{},
		Event: map[string]any{"id": "ev1"},
	})
	require.Error(t, err, "the event is part of the frozen run record")
}

func TestEventValue_Unconvertible(t *testing.T) {
	assert.Equal(t, "None", eventValue(map[string]any{"bad": make(chan int)}).String())
}
