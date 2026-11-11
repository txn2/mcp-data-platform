package generalfeedback

import (
	"context"
	"slices"
	"time"
)

// Config is the configuration file's portal.feedback block.
type Config struct {
	// StandaloneRecipients seeds the list until an administrator saves one:
	// each entry an email address, "persona:<name>" or "channel:<name>".
	// Empty seeds the administrator persona.
	StandaloneRecipients []string `yaml:"standalone_recipients"`
}

// PersonaOption is a persona the list can name, with how many people it
// reaches now.
type PersonaOption struct {
	Name        string `json:"name" example:"admin"`
	DisplayName string `json:"display_name" example:"Administrator"`
	People      int    `json:"people" example:"3"`
}

// View is what Admin > Settings > General feedback shows.
type View struct {
	Settings
	// People is everyone a new post reaches now, sorted.
	People []string `json:"people"`
	// PersonaOptions are the personas the list may name, with their people.
	PersonaOptions []PersonaOption `json:"persona_options"`
	// Warnings name what the saved list gets wrong: reaches_nobody.
	Warnings  []string   `json:"warnings"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// View reads the list with what it resolves to.
func (s *Service) View(ctx context.Context) View {
	st := s.stored(ctx)
	v := View{Settings: st.Value, People: s.people(ctx, st.Value), Warnings: []string{}, PersonaOptions: []PersonaOption{}}
	slices.Sort(v.People)
	if v.People == nil {
		v.People = []string{}
	}
	if len(v.People) == 0 && len(st.Value.Channels) == 0 {
		v.Warnings = append(v.Warnings, WarningReachesNobody)
	}
	if s.d.Personas != nil {
		for _, p := range s.d.Personas() {
			if s.d.Members != nil {
				p.People = len(s.d.Members(ctx, p.Name))
			}
			v.PersonaOptions = append(v.PersonaOptions, p)
		}
	}
	if st.UpdatedBy != "" {
		v.UpdatedBy = st.UpdatedBy
		at := st.UpdatedAt
		v.UpdatedAt = &at
	}
	ensureLists(&v.Settings)
	return v
}

// ensureLists writes an empty list as [] rather than null.
func ensureLists(s *Settings) {
	if s.Personas == nil {
		s.Personas = []string{}
	}
	if s.Recipients == nil {
		s.Recipients = []string{}
	}
	if s.Channels == nil {
		s.Channels = []string{}
	}
}
