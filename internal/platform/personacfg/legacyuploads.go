package personacfg

import (
	"context"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/internal/platform/configwarn"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// LegacyUploads is the personas a configuration file still gives #2258's
// uploads recipient lists (share_personas, share_emails), each with the line
// its mapping starts on. #2290 made uploads a switch and migration 000219
// read a stored list as on; a file naming one was read as off and logged as
// an unrecognized key, which left an external persona's people with no way
// to upload and nothing in the portal saying why (#2744). It is read as on,
// as the stored form is, and logged so the file is changed to uploads: true.
type LegacyUploads map[string]int

// ReadLegacyUploads finds every personas.<name>.portal.uploads that is a
// mapping.
func ReadLegacyUploads(expanded []byte) LegacyUploads {
	var doc yaml.Node
	if yaml.Unmarshal(expanded, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	personas := mappingValue(doc.Content[0], "personas")
	if personas == nil || personas.Kind != yaml.MappingNode {
		return nil
	}
	out := LegacyUploads{}
	for i := 0; i+1 < len(personas.Content); i += 2 {
		up := mappingValue(mappingValue(personas.Content[i+1], "portal"), "uploads")
		if up != nil && up.Kind == yaml.MappingNode {
			out[personas.Content[i].Value] = up.Line
		}
	}
	return out
}

// mappingValue is the value key holds in a mapping node, nil when n is not a
// mapping or holds no such key.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// Accepts reports whether a strict decode's unknown-key error is a legacy
// uploads mapping this release reads.
func (l LegacyUploads) Accepts(unknown string) bool {
	for _, line := range l {
		if unknown == fmt.Sprintf("line %d: cannot unmarshal !!map into bool", line) {
			return true
		}
	}
	return false
}

// Apply turns each legacy-uploads persona's uploads on and logs the personas.
func (l LegacyUploads) Apply(cfg *PersonasConfig) {
	if len(l) == 0 {
		return
	}
	names := make([]string, 0, len(l))
	on := true
	for name := range l {
		def, ok := cfg.Definitions[name]
		if !ok {
			continue
		}
		if def.Portal == nil {
			def.Portal = &persona.PortalAccess{}
		}
		def.Portal.Uploads = &on
		cfg.Definitions[name] = def
		names = append(names, name)
	}
	slices.Sort(names)
	configwarn.Warn(context.Background(), configwarn.CodeDeprecatedKey,
		"personas name #2258's uploads recipient lists, which are read as uploads: true; "+
			"replace the lists with uploads: true, since nothing is shared on arrival any more",
		"personas", names)
}
