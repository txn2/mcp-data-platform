package runstate

// RunAs is who a watch run acts as (#2635): the administrator who approved
// the version for handlers, with the roles they held then.
type RunAs struct {
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

// TriggerWatch marks a run a watch handler queued after a tool call it
// matched succeeded (#2635). It carries the call (run.call), runs with the
// roles of the administrator who approved the version, and only an
// administrator reads what it was given and printed: it holds another
// person's call. It sits beside script.TriggerPortal and the other triggers
// here so pkg/script's public surface does not grow.
const TriggerWatch = "watch"

// TriggerIntegration marks a run an integration queued (#2659): it calls the
// operation's function with the request in run.call, runs with the roles of
// the administrator who approved the version, and only an administrator reads
// what it was given and printed, since its error can repeat what the
// environment it calls answered.
const TriggerIntegration = "integration"

// AdminsOnly reports whether only an administrator reads what a run of
// trigger was given, printed and wrote: a watch run holds another person's
// call, and an integration's run what the environment it calls answered.
func AdminsOnly(trigger string) bool { return trigger == TriggerWatch || trigger == TriggerIntegration }

// CanceledError is the error a canceled run records: who asked, or that it
// was asked for.
func CanceledError(by string) string {
	if by == "" {
		return "canceled on request"
	}
	return "canceled by " + by
}
