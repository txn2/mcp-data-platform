package toolhelp

import (
	"regexp"
	"slices"
	"strings"
)

// exampleWordMin is the shortest intent word matched against a schema; "a",
// "me" and "to" match everything.
const exampleWordMin = 3

// Example is one call of a tool that fits what a caller said it wants to
// do, read from the tool's input schema (#2809): for a tool that chooses its
// operation by a command or action enum, the value whose words best match the
// intent, every parameter that value requires (a placeholder naming its type),
// and any enum parameter whose value the intent names ("assets shared with me"
// gives manage_asset's action list with scope shared). A tool with no such
// enum gets its required parameters. Nil when the schema declares nothing.
func Example(schema map[string]any, intent string) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	words := intentWords(intent)
	required := stringSet(schema["required"])
	field, commands := commandEnum(props)
	if field == "" {
		ex := map[string]any{}
		for _, name := range sortedKeys(props) {
			if required[name] {
				ex[name] = "<" + typeOf(asMap(props[name])) + ">"
			}
		}
		fillNamedEnums(ex, props, "", nil, words)
		return ex
	}
	cmd := bestCommand(field, commands, props, required, words)
	ex := map[string]any{field: cmd}
	for _, name := range sortedKeys(props) {
		if name != field && neededFor(asMap(props[name]), required[name], cmd, commands) {
			ex[name] = "<" + typeOf(asMap(props[name])) + ">"
		}
	}
	fillNamedEnums(ex, props, field, commands, words)
	return ex
}

// neededFor reports a parameter an example of cmd carries: one required for
// cmd by the schema or by its description (#2826).
func neededFor(prop map[string]any, required bool, cmd string, commands []string) bool {
	desc, _ := prop["description"].(string)
	return requiredFor(useOf(desc, commands), required, cmd)
}

// HelpField is the argument help is answered on for a tool with this input
// schema: its command or action field, when that is an enum. ok is false for a
// tool help does not apply to.
func HelpField(schema map[string]any) (field string, ok bool) {
	props, _ := schema["properties"].(map[string]any)
	field, _ = commandEnum(props)
	return field, field != ""
}

// commandEnum is the field a tool chooses its operation by and its values, or
// "" when it has none.
func commandEnum(props map[string]any) (field string, values []string) {
	for _, f := range commandFields {
		if values := enumStrings(asMap(props[f])); len(values) > 0 {
			return f, values
		}
	}
	return "", nil
}

// bestCommand scores each command by the intent words its name and its own
// parameters hold, and by the enum values the intent names in a parameter
// whose description mentions it; ties go to the command listed first. A
// command's own parameters are those whose description opens by naming it and
// those it requires (#2826): a description that only mentions a command in
// passing ("for the 'search' action") does not lend it its words.
func bestCommand(field string, commands []string, props map[string]any, required map[string]bool, words []string) string {
	best, bestScore := commands[0], -1
	for _, c := range commands {
		score := 2 * countMatches(words, strings.ReplaceAll(c, "_", " "))
		for name, raw := range props {
			if name != field {
				score += parameterScore(paramRef{name: name, p: asMap(raw), required: required[name]}, c, commands, words)
			}
		}
		if score > bestScore {
			best, bestScore = c, score
		}
	}
	return best
}

// paramRef is one parameter of a schema: its name, its property and whether
// the schema requires it.
type paramRef struct {
	name     string
	p        map[string]any
	required bool
}

// parameterScore is what one parameter adds to command c's score: the intent
// words its name and description hold when it is one of c's own parameters,
// and 2 when it names no command, mentions c, and the intent names one of its
// enum values.
func parameterScore(ref paramRef, c string, commands, words []string) int {
	name, p, required := ref.name, ref.p, ref.required
	desc, _ := p["description"].(string)
	use := useOf(desc, commands)
	score := 0
	if slices.Contains(prefixNamed(desc, commands), c) || (len(use.named) > 0 && requiredFor(use, required, c)) {
		score += countMatches(words, name+" "+desc)
	}
	if len(use.named) == 0 && mentions(desc, c) && namedEnumValue(p, words) != "" {
		score += 2
	}
	return score
}

// fillNamedEnums sets each enum parameter (other than the command field) whose
// value the intent names, when it applies to the chosen command.
func fillNamedEnums(ex, props map[string]any, field string, commands, words []string) {
	cmd, _ := ex[field].(string)
	for _, name := range sortedKeys(props) {
		if name == field {
			continue
		}
		p := asMap(props[name])
		v := namedEnumValue(p, words)
		if v == "" {
			continue
		}
		desc, _ := p["description"].(string)
		named := commandsNamed(desc, commands)
		if cmd == "" || len(named) == 0 || slices.Contains(named, cmd) {
			ex[name] = v
		}
	}
}

// namedEnumValue is the first enum value of a property the intent names.
func namedEnumValue(p map[string]any, words []string) string {
	for _, v := range enumStrings(p) {
		for _, w := range words {
			if strings.EqualFold(v, w) {
				return v
			}
		}
	}
	return ""
}

// enumStrings reads a property's string enum values.
func enumStrings(p map[string]any) []string {
	raw, _ := p["enum"].([]any)
	var out []string
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// intentWords is the intent's words worth matching, lower case.
func intentWords(intent string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(intent), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_'
	}) {
		if len(w) >= exampleWordMin {
			out = append(out, w)
		}
	}
	return out
}

// countMatches is how many intent words text holds as whole words.
func countMatches(words []string, text string) int {
	n := 0
	for _, w := range words {
		if mentions(text, w) {
			n++
		}
	}
	return n
}

// mentions reports whether text holds word as a whole word, any case.
func mentions(text, word string) bool {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`).MatchString(text)
}

// asMap is a decoded JSON value as an object, empty when it is not one.
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
