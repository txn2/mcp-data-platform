package runstate

// The trigger and the status of the runs a webhook source's events enqueue
// (#2332), kept here rather than beside pkg/script's own so its public
// surface does not carry them.
const (
	// TriggerEvent marks a run enqueued for one event the script's webhook
	// source stored. It executes as a scheduled run does, under the script's
	// saved author roles, with the event as run.event.
	TriggerEvent = "event"
	// StatusSkippedBacklog is terminal, and never executed: an event arrived
	// for a script already holding as many pending event runs as the
	// deployment allows. The event is stored in its source, and the run says
	// it was not run and why.
	StatusSkippedBacklog = "skipped_backlog"
)
