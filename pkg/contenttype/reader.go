package contenttype

import (
	"bytes"
	"errors"
	"io"
)

// DetectStream classifies a stream without buffering it. It reads at most
// StructuredSniffLen bytes to run detection, then returns a reader that
// replays those bytes ahead of the untouched remainder, so the caller can go on
// streaming the body to storage.
//
// A read error other than EOF is returned to the caller along with a reader
// that still replays whatever was consumed, so no bytes are lost.
func DetectStream(declared string, body io.Reader) (string, io.Reader, error) {
	prefix := make([]byte, StructuredSniffLen)
	n, err := io.ReadFull(body, prefix)
	prefix = prefix[:n]

	replayed := io.MultiReader(bytes.NewReader(prefix), body)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Normalize(declared), replayed, err
	}
	return Detect(declared, prefix), replayed, nil
}
