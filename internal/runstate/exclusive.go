package runstate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExclusiveMode is whether a script's runs may overlap (#1986, #2209): the
// setting a script carries and the run queue reads.
type ExclusiveMode string

// The modes. The zero value is off.
const (
	// ExclusiveOff lets runs overlap.
	ExclusiveOff ExclusiveMode = "off"
	// ExclusiveRefuse holds the script to one open run and refuses another.
	ExclusiveRefuse ExclusiveMode = "refuse"
	// ExclusiveQueue accepts another run and holds it pending until no other
	// run of the script is running.
	ExclusiveQueue ExclusiveMode = "queue"
)

// ParseExclusive reads a mode from any form a caller sends it in: the mode's
// name, or a boolean, which is what the setting was before it had a third
// value (true is refuse, false is off).
func ParseExclusive(v any) (ExclusiveMode, error) {
	switch val := v.(type) {
	case bool:
		if val {
			return ExclusiveRefuse, nil
		}
		return ExclusiveOff, nil
	case string:
		switch m := ExclusiveMode(strings.ToLower(strings.TrimSpace(val))); m {
		case ExclusiveOff, ExclusiveRefuse, ExclusiveQueue:
			return m, nil
		case "true":
			return ExclusiveRefuse, nil
		case "false", "":
			return ExclusiveOff, nil
		}
	}
	return "", fmt.Errorf(`exclusive must be "off", "refuse" or "queue" (true and false are read as refuse and off); got %v`, v)
}

// Or is the mode, with the zero value read as off.
func (m ExclusiveMode) Or() ExclusiveMode {
	if m == "" {
		return ExclusiveOff
	}
	return m
}

// UnmarshalJSON reads the mode in every form ParseExclusive takes.
func (m *ExclusiveMode) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("exclusive: %w", err)
	}
	parsed, err := ParseExclusive(raw)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// MarshalJSON writes the mode's name, off for the zero value.
func (m ExclusiveMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(m.Or())) //nolint:wrapcheck // a string always marshals
}
