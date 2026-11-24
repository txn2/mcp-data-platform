package sitebuild

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Report is what one entry's compile produced, from what (#2783): each output
// with its size before and after gzip, the inputs that contribute most to it,
// and the URLs it loads when the page starts.
type Report struct {
	Entry   string       `json:"entry"`
	Outputs []OutputSize `json:"outputs"`
	// LargestInputs are the modules contributing most bytes, at most ten:
	// paths of the site, and URLs fetched in bundle mode.
	LargestInputs []InputSize `json:"largest_inputs"`
	// StartImports are the URLs the entry imports when it is loaded, not the
	// ones a chunk imports when it is first used.
	StartImports []string `json:"start_imports"`
	// Warnings are the budgets the entry's first load exceeds.
	Warnings []string `json:"warnings,omitempty"`
}

// OutputSize is one output's size.
type OutputSize struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	GzipBytes int64  `json:"gzip_bytes"`
	// Chunk is true for code loaded when first used, under _chunks/.
	Chunk bool `json:"chunk,omitempty"`
	// Previous and Change compare it with the release before, when that
	// release served an output at the same path; an output whose name carries
	// a hash is compared by the name without it.
	PreviousBytes *int64 `json:"previous_bytes,omitempty"`
	Change        string `json:"change,omitempty"`
}

// InputSize is one module's contribution to an entry's outputs.
type InputSize struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// maxLargest is how many inputs a report names.
const maxLargest = 10

// metafile is the part of esbuild's metafile a report reads.
type metafile struct {
	Outputs map[string]struct {
		Inputs map[string]struct {
			BytesInOutput int64 `json:"bytesInOutput"`
		} `json:"inputs"`
		Imports    []metaImport `json:"imports"`
		EntryPoint string       `json:"entryPoint"`
	} `json:"outputs"`
}

// metaImport is one import an output makes.
type metaImport struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	External bool   `json:"external"`
}

// condense reads a compile's metafile into its report.
func condense(entry, raw string, outputs []Output, cfg *Config) (*Report, error) {
	var meta metafile
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, fmt.Errorf("reading what the compiler produced: %w", err)
	}
	rep := &Report{Entry: entry, Outputs: []OutputSize{}, LargestInputs: []InputSize{}, StartImports: []string{}}
	for _, o := range outputs {
		if strings.HasSuffix(o.Path, ".map") {
			continue
		}
		rep.Outputs = append(rep.Outputs, OutputSize{Path: o.Path, Bytes: int64(len(o.Body)), GzipBytes: gzipSize(o.Body), Chunk: strings.HasPrefix(o.Path, ChunkDir+sep)})
	}
	contrib := map[string]int64{}
	for name, out := range meta.Outputs {
		for in, v := range out.Inputs {
			contrib[inputName(in)] += v.BytesInOutput
		}
		if out.EntryPoint != "" && !strings.HasSuffix(name, extCSS) {
			rep.StartImports = append(rep.StartImports, startImports(out.Imports)...)
		}
	}
	for in, n := range contrib {
		rep.LargestInputs = append(rep.LargestInputs, InputSize{Path: in, Bytes: n})
	}
	slices.SortFunc(rep.LargestInputs, func(a, b InputSize) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Path, b.Path)
	})
	rep.LargestInputs = rep.LargestInputs[:min(len(rep.LargestInputs), maxLargest)]
	slices.Sort(rep.StartImports)
	rep.StartImports = slices.Compact(rep.StartImports)
	rep.Warnings = budgetWarnings(entry, rep, cfg.Budgets[entry])
	return rep, nil
}

// startImports are the URLs an entry's script imports when it loads.
func startImports(imports []metaImport) []string {
	var out []string
	for _, imp := range imports {
		if imp.External && imp.Kind == "import-statement" && strings.HasPrefix(imp.Path, "https://") {
			out = append(out, imp.Path)
		}
	}
	return out
}

// inputName is a module as a person names it: the site's path or the URL.
func inputName(in string) string {
	for _, ns := range []string{nsSite, nsURL, nsCSSURL} {
		if rest, ok := strings.CutPrefix(in, ns+":"); ok {
			return strings.TrimPrefix(rest, sep)
		}
	}
	return strings.TrimPrefix(in, sep)
}

func gzipSize(body []byte) int64 {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return 0
	}
	_, _ = zw.Write(body)
	_ = zw.Close()
	return int64(buf.Len())
}

// budgetWarnings are the sizes of an entry's first load past its budget: its
// script and its stylesheet, not the chunks it loads later.
func budgetWarnings(entry string, rep *Report, b Budget) []string {
	js, css := OutputOf(entry), strings.TrimSuffix(OutputOf(entry), extJS)+extCSS
	var out []string
	for _, o := range rep.Outputs {
		limits := map[string][2]Size{}
		switch o.Path {
		case js:
			limits["js"], limits["js_gzip"] = [2]Size{b.JS, Size(o.Bytes)}, [2]Size{b.JSGzip, Size(o.GzipBytes)}
		case css:
			limits["css"], limits["css_gzip"] = [2]Size{b.CSS, Size(o.Bytes)}, [2]Size{b.CSSGzip, Size(o.GzipBytes)}
		}
		for _, name := range []string{"js", "js_gzip", "css", "css_gzip"} {
			l, ok := limits[name]
			if ok && l[0] > 0 && l[1] > l[0] {
				out = append(out, fmt.Sprintf("%s: %s is %s, over its %s budget of %s", entry, o.Path, FormatBytes(int64(l[1])), name, FormatBytes(int64(l[0]))))
			}
		}
	}
	return out
}

// FormatBytes writes a size as a person reads it.
func FormatBytes(n int64) string {
	const kb, mb = int64(1) << 10, int64(1) << 20
	switch {
	case n >= mb:
		return fmt.Sprintf("%.2f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	}
	return fmt.Sprintf("%d B", n)
}

// Compare fills in each output's change from the release before, read from
// that release's reports: by path, or for a chunk by its name without the
// hash, since a chunk whose content changed has a new name.
func (r *Report) Compare(previous []Report) {
	before := map[string]int64{}
	for _, p := range previous {
		for _, o := range p.Outputs {
			before[stableName(o.Path)] = o.Bytes
		}
	}
	for i := range r.Outputs {
		o := &r.Outputs[i]
		prev, ok := before[stableName(o.Path)]
		if !ok {
			continue
		}
		o.PreviousBytes = &prev
		o.Change = fmt.Sprintf("%s %s → %s (%s)", o.Path, FormatBytes(prev), FormatBytes(o.Bytes), signed(o.Bytes-prev))
	}
}

func signed(d int64) string {
	if d < 0 {
		return "-" + FormatBytes(-d)
	}
	return "+" + FormatBytes(d)
}

// stableName is a chunk's path without its hash: _chunks/Reports-AB12CD34.js
// is _chunks/Reports.js.
func stableName(p string) string {
	if !strings.HasPrefix(p, ChunkDir+sep) {
		return p
	}
	ext := ""
	if i := strings.Index(p[len(ChunkDir)+1:], dot); i >= 0 {
		ext = p[len(ChunkDir)+1+i:]
	}
	stem := strings.TrimSuffix(p, ext)
	if i := strings.LastIndex(stem, "-"); i > len(ChunkDir) {
		stem = stem[:i]
	}
	return stem + ext
}
