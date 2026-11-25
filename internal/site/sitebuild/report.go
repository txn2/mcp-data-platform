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
//
// What loads at start is the entry's script and the shared chunks it imports
// statically, transitively (#2791). A chunk reached only through import() is
// loaded when first used, and is left out of the start figures.
type Report struct {
	Entry   string       `json:"entry"`
	Outputs []OutputSize `json:"outputs"`
	// LargestInputs are the modules contributing most bytes to what loads at
	// start, at most ten: paths of the site, and URLs fetched in bundle mode.
	LargestInputs []InputSize `json:"largest_inputs"`
	// LargestInputsLazy are the modules contributing most bytes to the chunks
	// loaded when first used, at most ten.
	LargestInputsLazy []InputSize `json:"largest_inputs_lazy,omitempty"`
	// StartImports are the URLs the entry and the chunks it imports statically
	// import when the page loads, not the ones a chunk loaded when first used
	// imports.
	StartImports []string `json:"start_imports"`
	// StartBytes and StartGzipBytes are the size of the scripts that load at
	// start: the entry's script and the chunks it imports statically. The js
	// and js_gzip budgets are judged against them.
	StartBytes     int64 `json:"start_bytes"`
	StartGzipBytes int64 `json:"start_gzip_bytes"`
	// Warnings are the budgets the entry's first load exceeds.
	Warnings []string `json:"warnings,omitempty"`

	// startChunks is how many scripts besides the entry's load at start.
	startChunks int
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
	start := meta.startSet(metaKey(OutputOf(entry)))
	for _, o := range outputs {
		if strings.HasSuffix(o.Path, ".map") {
			continue
		}
		size := OutputSize{Path: o.Path, Bytes: int64(len(o.Body)), GzipBytes: gzipSize(o.Body), Chunk: strings.HasPrefix(o.Path, ChunkDir+sep)}
		rep.Outputs = append(rep.Outputs, size)
		if start[metaKey(o.Path)] && !strings.HasSuffix(o.Path, extCSS) {
			rep.StartBytes += size.Bytes
			rep.StartGzipBytes += size.GzipBytes
			if o.Path != OutputOf(entry) {
				rep.startChunks++
			}
		}
	}
	atStart, lazy := map[string]int64{}, map[string]int64{}
	for name, out := range meta.Outputs {
		into := lazy
		if start[name] {
			into = atStart
			if !strings.HasSuffix(name, extCSS) {
				rep.StartImports = append(rep.StartImports, startImports(out.Imports)...)
			}
		}
		for in, v := range out.Inputs {
			into[inputName(in)] += v.BytesInOutput
		}
	}
	rep.LargestInputs = largest(atStart)
	if l := largest(lazy); len(l) > 0 {
		rep.LargestInputsLazy = l
	}
	slices.Sort(rep.StartImports)
	rep.StartImports = slices.Compact(rep.StartImports)
	rep.Warnings = budgetWarnings(entry, rep, cfg.Budgets[entry])
	return rep, nil
}

// metaKey is an output's path as the metafile names it.
func metaKey(outputPath string) string {
	return strings.TrimPrefix(outDir, sep) + outputPath
}

// startSet is the outputs that load when the page starts: the entry's output
// and every output it imports statically, transitively. An output reached only
// through import() is not in it, though esbuild gives it an entryPoint of its
// own (#2791).
func (m metafile) startSet(entry string) map[string]bool {
	set := map[string]bool{}
	queue := []string{entry}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		out, ok := m.Outputs[name]
		if !ok || set[name] {
			continue
		}
		set[name] = true
		for _, imp := range out.Imports {
			if !imp.External && imp.Kind == "import-statement" {
				queue = append(queue, imp.Path)
			}
		}
	}
	return set
}

// largest is the inputs contributing most bytes, at most maxLargest.
func largest(contrib map[string]int64) []InputSize {
	out := make([]InputSize, 0, len(contrib))
	for in, n := range contrib {
		out = append(out, InputSize{Path: in, Bytes: n})
	}
	slices.SortFunc(out, func(a, b InputSize) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out[:min(len(out), maxLargest)]
}

// startImports are the URLs an output imports when it loads.
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

// budgetWarnings are the sizes of an entry's first load past its budget: the
// scripts that load at start (its own and the chunks it imports statically,
// #2791) and its stylesheet, not the chunks it loads when first used.
func budgetWarnings(entry string, rep *Report, b Budget) []string {
	js, css := OutputOf(entry), strings.TrimSuffix(OutputOf(entry), extJS)+extCSS
	scripts := js
	if rep.startChunks > 0 {
		scripts = fmt.Sprintf("%s with the %d chunk(s) it imports at start", js, rep.startChunks)
	}
	var out []string
	warn := func(what string, budget Size, size int64, name string) {
		if budget > 0 && Size(size) > budget {
			out = append(out, fmt.Sprintf("%s: %s is %s, over its %s budget of %s", entry, what, FormatBytes(size), name, FormatBytes(int64(budget))))
		}
	}
	warn(scripts, b.JS, rep.StartBytes, "js")
	warn(scripts, b.JSGzip, rep.StartGzipBytes, "js_gzip")
	for _, o := range rep.Outputs {
		if o.Path == css {
			warn(css, b.CSS, o.Bytes, "css")
			warn(css, b.CSSGzip, o.GzipBytes, "css_gzip")
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
