package siterender

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// The folders of a source tree that are read, not served.
const (
	layoutsDir = "_layouts"
	dataDir    = "_data"
)

const (
	frontMatterFence = "---"
	extJSON          = ".json"
	extMarkdownShort = ".md"
	extMarkdown      = ".markdown"
)

// What one document may hold: how deeply it may nest, and how many keys one
// object may have, since a for loop sorts them.
const (
	maxDataDepth = 64
	maxDataKeys  = 4096
)

// templated are the extensions that can carry front matter. Anything else is
// copied without being opened.
var templated = []string{".html", ".htm", extMarkdownShort, extMarkdown, ".xml", extJSON, ".txt"}

func isTemplated(name string) bool {
	return slices.Contains(templated, strings.ToLower(path.Ext(name)))
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == extMarkdownShort || ext == extMarkdown
}

// Active reports whether a source tree is rendered at all: a tree with no
// _layouts folder is served exactly as it was published.
func Active(src fs.FS) bool {
	info, err := fs.Stat(src, layoutsDir)
	return err == nil && info.IsDir()
}

// sortedMap is a mapping as templates see it. A plain Go map iterates in random
// order, so a for loop over one would make a render differ from itself.
type sortedMap map[string]any

// Len and Index make for iterate the keys in order, as [key, value] pairs.
func (m sortedMap) Len() int { return len(m) }

// Index is the i-th pair in key order.
func (m sortedMap) Index(i int) any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return []any{keys[i], m[keys[i]]}
}

// settle turns a decoded document into what templates may hold: maps in a fixed
// order, and nothing nested deeply enough to overflow the stack rendering it.
func settle(v any, depth int) (any, error) {
	if depth > maxDataDepth {
		return nil, fmt.Errorf("nested more than %d deep", maxDataDepth)
	}
	switch val := v.(type) {
	case map[string]any:
		return settleMap(val, depth)
	case []any:
		return settleList(val, depth)
	}
	return v, nil
}

func settleMap(val map[string]any, depth int) (any, error) {
	if len(val) > maxDataKeys {
		return nil, fmt.Errorf("an object has %d keys, more than the %d allowed; make it a list", len(val), maxDataKeys)
	}
	out := make(sortedMap, len(val))
	for k, item := range val {
		settled, err := settle(item, depth+1)
		if err != nil {
			return nil, err
		}
		out[k] = settled
	}
	return out, nil
}

func settleList(val []any, depth int) (any, error) {
	out := make([]any, len(val))
	for i, item := range val {
		settled, err := settle(item, depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = settled
	}
	return out, nil
}

// document is a file split into its front matter and body.
type document struct {
	meta sortedMap
	body []byte
}

// splitFrontMatter separates a document's YAML header from its body. A document
// with none returns nil, and is then an ordinary file.
func splitFrontMatter(doc []byte) (*document, error) {
	line, rest, found := bytes.Cut(doc, []byte("\n"))
	if !found || string(bytes.TrimRight(line, " \r")) != frontMatterFence {
		return nil, nil //nolint:nilnil // no front matter is the ordinary case
	}
	header, tail, err := cutHeader(rest)
	if err != nil {
		return nil, err
	}
	var body []byte
	if _, after, cut := bytes.Cut(tail, []byte("\n")); cut {
		body = after
	}
	meta, err := decodeYAML(header)
	if err != nil {
		return nil, err
	}
	return &document{meta: meta, body: body}, nil
}

// cutHeader splits what follows the opening fence at the closing one.
func cutHeader(rest []byte) (header, tail []byte, err error) {
	if bytes.HasPrefix(rest, []byte(frontMatterFence)) {
		return nil, rest[len(frontMatterFence):], nil
	}
	end := bytes.Index(rest, []byte("\n"+frontMatterFence))
	if end < 0 {
		return nil, nil, errors.New("front matter has no closing ---")
	}
	return rest[:end], rest[end+1+len(frontMatterFence):], nil
}

func decodeYAML(data []byte) (sortedMap, error) {
	decoded := map[string]any{}
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	settled, err := settle(decoded, 0)
	if err != nil {
		return nil, err
	}
	m, _ := settled.(sortedMap)
	return m, nil
}

// readFile reads one source file, refusing one too large to hold.
func (r *run) readFile(name string) ([]byte, error) {
	info, err := fs.Stat(r.src, name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	if info.Size() > r.limits.TemplateBytes {
		return nil, fmt.Errorf("file %s is %d bytes, more than the %d a template or record may be", name, info.Size(), r.limits.TemplateBytes)
	}
	body, err := fs.ReadFile(r.src, name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return body, nil
}

// hold charges what a render keeps for its whole length: its records and front
// matter. A page's body is read again when it is rendered and is not held.
func (r *run) hold(n int) error {
	r.held += int64(n)
	if r.held > r.limits.DataBytes {
		return &BudgetError{Resource: "bytes of records and front matter", Limit: r.limits.DataBytes}
	}
	return nil
}

// decodeRecord reads one record file: JSON, YAML, or Markdown with front matter.
func (r *run) decodeRecord(name string) (sortedMap, error) {
	data, err := r.readFile(name)
	if err != nil {
		return nil, err
	}
	if err := r.hold(len(data)); err != nil {
		return nil, err
	}
	record, err := parseRecord(name, data)
	if err != nil {
		return nil, fmt.Errorf("record %s: %w", name, err)
	}
	if _, set := record["slug"]; !set {
		record["slug"] = strings.TrimSuffix(path.Base(name), path.Ext(name))
	}
	record[recordFile] = name
	return record, nil
}

func parseRecord(name string, data []byte) (sortedMap, error) {
	switch strings.ToLower(path.Ext(name)) {
	case extJSON:
		decoded, err := decodeJSON(data)
		if err != nil {
			return nil, fmt.Errorf("not a JSON object: %w", err)
		}
		settled, err := settle(decoded, 0)
		if err != nil {
			return nil, err
		}
		m, _ := settled.(sortedMap)
		return m, nil
	case ".yaml", ".yml":
		return decodeYAML(data)
	}
	doc, err := splitFrontMatter(data)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		doc = &document{meta: sortedMap{}, body: data}
	}
	html, err := toHTML(doc.body)
	if err != nil {
		return nil, err
	}
	doc.meta["content"] = html
	return doc.meta, nil
}

// collection is one folder of _data. It is a lazy value: the first template that
// reads it loads its records, and that read is what makes the page depend on it.
type collection struct {
	run     *run
	name    string
	records []any
	loaded  bool
	err     error
}

// ToLiquid is what the engine calls when a template first touches the value.
func (c *collection) ToLiquid() any {
	c.run.dep(inputData + c.name)
	if err := c.load(); err != nil {
		c.run.fail(c.err)
		return []any{}
	}
	return c.records
}

func (c *collection) load() error {
	if c.loaded {
		return c.err
	}
	c.loaded = true
	dir := path.Join(dataDir, c.name)
	entries, err := fs.ReadDir(c.run.src, dir)
	if err != nil {
		c.err = fmt.Errorf("reading %s: %w", dir, err)
		return c.err
	}
	c.records = make([]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isRecord(entry.Name()) {
			continue
		}
		record, err := c.run.decodeRecord(path.Join(dir, entry.Name()))
		if err != nil {
			c.err = err
			return err
		}
		c.records = append(c.records, record)
	}
	return nil
}

func isRecord(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case extJSON, ".yaml", ".yml", extMarkdownShort, extMarkdown:
		return true
	}
	return false
}
