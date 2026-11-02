package sourcecheck

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"oss.terrastruct.com/d2/d2ast"
	"oss.terrastruct.com/d2/d2compiler"
	"oss.terrastruct.com/d2/d2parser"
)

// d2Path is the file name a D2 source is parsed under, which its errors
// begin with.
const d2Path = "diagram.d2"

// anyURL finds a URL in a D2 value: a scheme followed by ://, or a data: URI.
var anyURL = regexp.MustCompile(`(?i)\b(?:[a-z][a-z0-9+.-]*://|data:)[^\s"'<>)]*`)

// checkD2 refuses a D2 source that does not compile, one that imports another
// file (a stored diagram has none beside it), and one naming any URL other
// than a managed resource by its mcp:// URI or an inline data: image (#2302).
// The platform renders the source to SVG, and an icon or link naming a host
// would make every reader's browser fetch it.
func checkD2(body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("the D2 source is empty; write a diagram such as a -> b")
	}
	ast, err := d2parser.Parse(d2Path, strings.NewReader(body), nil)
	if err != nil {
		return fmt.Errorf("the D2 source does not parse: %w", err)
	}
	var imports int
	var urls []string
	d2ast.Walk(ast, func(n d2ast.Node) bool {
		switch v := n.(type) {
		case *d2ast.Import:
			imports++
		case d2ast.String:
			urls = append(urls, refusedURLs(v.ScalarString())...)
		}
		return true
	})
	if imports > 0 {
		return errors.New("the D2 source imports another file; a stored diagram has no files beside it, so write it in one source")
	}
	if len(urls) > 0 {
		return fmt.Errorf("the D2 source names %s, which is not a managed resource. "+
			"An icon or image is a managed resource by its mcp:// URI, declared in references, or an inline data: image", named(urls))
	}
	if _, _, err := d2compiler.Compile(d2Path, strings.NewReader(body), nil); err != nil {
		return fmt.Errorf("the D2 source does not compile: %w", err)
	}
	return nil
}

// refusedURLs returns the URLs in a value that are neither a managed resource
// reference nor an inline data: image.
func refusedURLs(s string) []string {
	var out []string
	for _, u := range anyURL.FindAllString(s, -1) {
		lower := strings.ToLower(u)
		if isReference(lower) || strings.HasPrefix(lower, "data:image/") {
			continue
		}
		out = append(out, u)
	}
	return out
}
