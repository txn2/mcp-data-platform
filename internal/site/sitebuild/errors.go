package sitebuild

import (
	"fmt"
	"strings"
)

// Message is one place a compile could not get past.
type Message struct {
	File   string
	Line   int
	Column int
	Text   string
}

// Error is every message of a failed compile.
type Error struct {
	Messages []Message
}

func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Messages))
	for _, m := range e.Messages {
		parts = append(parts, fmt.Sprintf("%s:%d:%d: %s", m.File, m.Line, m.Column, m.Text))
	}
	return "the build failed: " + strings.Join(parts, "; ")
}

// LimitError is a compile stopped for reading or writing more than it may.
type LimitError struct {
	Entry    string
	Resource string
	Limit    int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s exceeded the limit of %d %s", e.Entry, e.Limit, e.Resource)
}
