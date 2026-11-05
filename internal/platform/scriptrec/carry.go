package scriptrec

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/jsonwalk"
)

// Renamed returns a recording with every string in its run parameters and in
// each tool call's arguments passed through rename, and the key of each call
// whose arguments changed rebuilt from them, so a replay of a source whose
// names were mapped the same way finds its calls. An application import
// carries a script's recordings to a deployment where the connections and
// secrets it names are mapped to other names (#2476). A call rename leaves
// as it was keeps its key as recorded, and a recording nothing in changes is
// returned as it was given.
func Renamed(data []byte, rename func(string) string) ([]byte, error) {
	rec, err := Decode(data)
	if err != nil {
		return nil, err
	}
	renamed := false
	if p, changed := jsonwalk.Strings(rec.Params, rename); changed {
		rec.Params, _ = p.(map[string]any)
		renamed = true
	}
	for i := range rec.Calls {
		c := &rec.Calls[i]
		if c.Tool == "" {
			continue
		}
		if args, changed := jsonwalk.Strings(c.Args, rename); changed {
			c.Args, _ = args.(map[string]any)
			c.Key = ToolKey(c.Tool, c.Args)
			renamed = true
		}
	}
	if !renamed {
		return data, nil
	}
	return Encode(rec)
}

// Same reports whether two recordings hold the same header and calls, however
// each was compressed. One that does not read is the same as nothing.
func Same(a, b []byte) bool {
	ra, errA := Decode(a)
	rb, errB := Decode(b)
	if errA != nil || errB != nil {
		return false
	}
	ja, errA := json.Marshal(ra)
	jb, errB := json.Marshal(rb)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// Encode writes a recording in the layout Decode reads, refusing one over
// MaxBytes compressed.
func Encode(rec *Recording) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	h := rec.Header
	h.V = formatVersion
	lines := make([]any, 0, len(rec.Calls))
	lines = append(lines, h)
	for _, c := range rec.Calls {
		lines = append(lines, c)
	}
	for _, v := range lines {
		line, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encoding the recording: %w", err)
		}
		if _, err := gz.Write(append(line, '\n')); err != nil {
			return nil, fmt.Errorf("encoding the recording: %w", err)
		}
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("encoding the recording: %w", err)
	}
	if buf.Len() > MaxBytes {
		return nil, fmt.Errorf("the recording is %d bytes, over the %d MiB a recording may hold", buf.Len(), MaxBytes>>20)
	}
	return buf.Bytes(), nil
}
