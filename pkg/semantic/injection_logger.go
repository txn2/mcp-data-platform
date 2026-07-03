package semantic

import "log/slog"

// DetectAndLogInjection checks input for prompt-injection patterns and emits
// a structured slog warning for each detection, so operators can search the
// log stream by entity source, field, and matched patterns. Returns true when
// patterns were detected.
func DetectAndLogInjection(sanitizer *Sanitizer, source, field, input string) bool {
	if input == "" {
		return false
	}

	detected, patterns := sanitizer.DetectInjection(input)
	if !detected {
		return false
	}

	slog.Warn("prompt injection patterns detected",
		"source", source,
		"field", field,
		"patterns", patterns)
	return true
}
