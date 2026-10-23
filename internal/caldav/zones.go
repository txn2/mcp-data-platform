package caldav

import (
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// paramUnresolved marks a property whose TZID the platform could not map to
// an IANA zone, holding the name the event used. It is the platform's own
// parameter, set by normalize and never sent anywhere.
const paramUnresolved = "X-MCP-UNRESOLVED-TZID"

// zoneResolver maps the zone names a calendar writes to IANA zones. A TZID is
// free text: Google and Apple write IANA names, Exchange writes Windows names
// ("W. Europe Standard Time"), some clients write a path ending in one
// ("/mozilla.org/20050126_1/Europe/Berlin"), and a VTIMEZONE may say which
// zone it is in X-LIC-LOCATION. The object's VTIMEZONE rules themselves are
// not evaluated; the IANA database is the source of the offsets.
type zoneResolver struct {
	// licLocation maps a VTIMEZONE's TZID to its X-LIC-LOCATION.
	licLocation map[string]string
	cache       map[string]string
}

func newZoneResolver(cal *ical.Calendar) *zoneResolver {
	z := &zoneResolver{licLocation: map[string]string{}, cache: map[string]string{}}
	for _, c := range cal.Children {
		if c.Name != ical.CompTimezone {
			continue
		}
		id := rawValue(c, ical.PropTimezoneID)
		if loc := rawValue(c, "X-LIC-LOCATION"); id != "" && loc != "" {
			z.licLocation[id] = loc
		}
	}
	return z
}

// normalize rewrites every TZID parameter under comp to the IANA name it
// resolves to, so go-ical's own time parsing finds it. A name that resolves to
// nothing is replaced with UTC and recorded in paramUnresolved.
func (z *zoneResolver) normalize(comp *ical.Component) {
	for name, props := range comp.Props {
		for i := range props {
			tz := props[i].Params.Get(ical.PropTimezoneID)
			if tz == "" {
				continue
			}
			if iana := z.resolve(tz); iana != "" {
				props[i].Params.Set(ical.PropTimezoneID, iana)
				continue
			}
			props[i].Params.Set(ical.PropTimezoneID, zoneUTC)
			props[i].Params.Set(paramUnresolved, tz)
		}
		comp.Props[name] = props
	}
	for _, c := range comp.Children {
		z.normalize(c)
	}
}

// unresolved is the zone name p used that normalize could not resolve, or "".
func (*zoneResolver) unresolved(p *ical.Prop) string {
	if p == nil {
		return ""
	}
	return p.Params.Get(paramUnresolved)
}

func (z *zoneResolver) resolve(tz string) string {
	if v, ok := z.cache[tz]; ok {
		return v
	}
	v := z.lookup(tz)
	z.cache[tz] = v
	return v
}

func (z *zoneResolver) lookup(tz string) string {
	candidates := []string{strings.TrimSpace(tz), strings.Trim(tz, `"`)}
	if loc := z.licLocation[tz]; loc != "" {
		candidates = append(candidates, loc)
	}
	if iana, ok := windowsZones[strings.TrimSpace(tz)]; ok {
		candidates = append(candidates, iana)
	}
	// A path: try each suffix, longest first, so "/a/b/Europe/Berlin" finds
	// "Europe/Berlin" and "America/Argentina/Buenos_Aires" survives whole.
	parts := strings.Split(strings.Trim(tz, "/"), "/")
	for i := 1; i < len(parts); i++ {
		candidates = append(candidates, strings.Join(parts[i:], "/"))
	}
	for _, c := range candidates {
		if c == "" || strings.EqualFold(c, "local") {
			continue
		}
		if _, err := time.LoadLocation(c); err == nil {
			return c
		}
	}
	return ""
}

// windowsZones maps the Windows zone names Exchange and Outlook write to the
// IANA zone CLDR names as their primary (windowsZones.xml, territory 001). It
// carries the zones a calendar is most likely to name, not all of them; an
// absent name is reported unresolved rather than guessed.
var windowsZones = map[string]string{ //nolint:gochecknoglobals // fixed CLDR table
	"Dateline Standard Time":          "Etc/GMT+12",
	"Hawaiian Standard Time":          "Pacific/Honolulu",
	"Alaskan Standard Time":           "America/Anchorage",
	"Pacific Standard Time":           "America/Los_Angeles",
	"US Mountain Standard Time":       "America/Phoenix",
	"Mountain Standard Time":          "America/Denver",
	"Central America Standard Time":   "America/Guatemala",
	"Central Standard Time":           "America/Chicago",
	"Central Standard Time (Mexico)":  "America/Mexico_City",
	"Canada Central Standard Time":    "America/Regina",
	"SA Pacific Standard Time":        "America/Bogota",
	"Eastern Standard Time":           "America/New_York",
	"US Eastern Standard Time":        "America/Indianapolis",
	"Venezuela Standard Time":         "America/Caracas",
	"Atlantic Standard Time":          "America/Halifax",
	"SA Western Standard Time":        "America/La_Paz",
	"Pacific SA Standard Time":        "America/Santiago",
	"Newfoundland Standard Time":      "America/St_Johns",
	"E. South America Standard Time":  "America/Sao_Paulo",
	"Argentina Standard Time":         "America/Buenos_Aires",
	"Greenland Standard Time":         "America/Godthab",
	zoneUTC:                           zoneUTC,
	"GMT Standard Time":               "Europe/London",
	"Greenwich Standard Time":         "Atlantic/Reykjavik",
	"W. Europe Standard Time":         "Europe/Berlin",
	"Central Europe Standard Time":    "Europe/Budapest",
	"Romance Standard Time":           "Europe/Paris",
	"Central European Standard Time":  "Europe/Warsaw",
	"W. Central Africa Standard Time": "Africa/Lagos",
	"GTB Standard Time":               "Europe/Bucharest",
	"E. Europe Standard Time":         "Europe/Chisinau",
	"South Africa Standard Time":      "Africa/Johannesburg",
	"FLE Standard Time":               "Europe/Kiev",
	"Israel Standard Time":            "Asia/Jerusalem",
	"Egypt Standard Time":             "Africa/Cairo",
	"Turkey Standard Time":            "Europe/Istanbul",
	"Arab Standard Time":              "Asia/Riyadh",
	"Russian Standard Time":           "Europe/Moscow",
	"E. Africa Standard Time":         "Africa/Nairobi",
	"Iran Standard Time":              "Asia/Tehran",
	"Arabian Standard Time":           "Asia/Dubai",
	"Pakistan Standard Time":          "Asia/Karachi",
	"India Standard Time":             "Asia/Calcutta",
	"Nepal Standard Time":             "Asia/Katmandu",
	"Bangladesh Standard Time":        "Asia/Dhaka",
	"SE Asia Standard Time":           "Asia/Bangkok",
	"China Standard Time":             "Asia/Shanghai",
	"Singapore Standard Time":         "Asia/Singapore",
	"W. Australia Standard Time":      "Australia/Perth",
	"Taipei Standard Time":            "Asia/Taipei",
	"Tokyo Standard Time":             "Asia/Tokyo",
	"Korea Standard Time":             "Asia/Seoul",
	"Cen. Australia Standard Time":    "Australia/Adelaide",
	"AUS Eastern Standard Time":       "Australia/Sydney",
	"E. Australia Standard Time":      "Australia/Brisbane",
	"New Zealand Standard Time":       "Pacific/Auckland",
}
