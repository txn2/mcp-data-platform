package catalogscan

import "strings"

// Unread is one part of a connection a scan could not read: the dataset path
// prefix in reference form, and why.
type Unread struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// UnreadError is what a source returns when it read the connection except
// for some parts of it.
type UnreadError struct {
	Parts []Unread
}

// Error lists each unread part with its reason.
func (e *UnreadError) Error() string {
	msgs := make([]string, len(e.Parts))
	for i, p := range e.Parts {
		msgs[i] = p.Path + ": " + p.Error
	}
	return "could not read " + strings.Join(msgs, "; ")
}
