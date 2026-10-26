package whsource

import (
	"strings"
)

// InputForm is a source whose events are a form's accepted submissions
// (#2178). The platform creates one when a form script is bound to an
// endpoint; it is never posted to, and no administrator creates one by hand.
const InputForm = "form"

// FormPrefix starts the name of every form's source.
const FormPrefix = "form-"

// Form is the form a form source collects for.
type Form struct {
	// ScriptID is the form script's id: the source follows the script, so an
	// endpoint renamed later still stores into the source made for it.
	ScriptID string `json:"script_id"`
	// Endpoint is the endpoint the form was bound to when its source was
	// made, which its name was derived from.
	Endpoint string `json:"endpoint"`
	// DecryptPersonas are the personas a script run may decrypt the form's
	// sensitive fields as (platform.decrypt, #2179); an administrator always
	// may, and so does the owner on the submissions view.
	DecryptPersonas []string `json:"decrypt_personas,omitempty"`
}

// IsForm reports whether the source collects a form's submissions.
func (s Source) IsForm() bool { return s.Config.Input == InputForm }

// FormSourceName is the name of the source a form bound at endpoint gets:
// FormPrefix and the endpoint's name with each "_" read as "-", which is the
// one character an endpoint name takes that a source name does not.
func FormSourceName(endpoint string) string {
	return FormPrefix + strings.ReplaceAll(endpoint, "_", "-")
}

// validateForm checks a form source: it names its script and endpoint, takes
// no secret, and its name is the one its endpoint gives it.
func validateForm(s Source) error {
	f := s.Config.Form
	switch {
	case f == nil || strings.TrimSpace(f.ScriptID) == "" || strings.TrimSpace(f.Endpoint) == "":
		return invalid("a form source names the script and endpoint it collects for")
	case s.Auth.Secret != "" || s.Auth.Mode != "":
		return invalid("a form source takes no auth: a submission is checked by its form")
	case s.Name != FormSourceName(f.Endpoint):
		return invalid("a form source bound at %q is named %q", f.Endpoint, FormSourceName(f.Endpoint))
	}
	return validateFormSettings(s)
}

// validateFormSettings checks a form source's personas, and that none of the
// paths that name an event's id, type and key is set.
func validateFormSettings(s Source) error {
	for _, p := range s.Config.Form.DecryptPersonas {
		if strings.TrimSpace(p) == "" {
			return invalid("config.form.decrypt_personas names an empty persona")
		}
	}
	if s.Config.EventIDPath != "" || s.Config.EventTypePath != "" || s.Config.KeyPath != "" || s.Config.Split != "" {
		// A form's event is built by the platform, so no path a field could
		// fill (a sensitive one among them) names its id, type or key.
		return invalid("a form source's event id, type, key and split are the platform's")
	}
	return nil
}

// refuseFormBlock refuses a form block on a source that is not a form's.
func refuseFormBlock(s Source) error {
	if s.Config.Form != nil {
		return invalid("config.form is only for a form's source, which the platform makes when a form is bound; this source's input is %s", s.Config.Input)
	}
	return nil
}
