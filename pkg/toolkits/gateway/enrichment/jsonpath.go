package enrichment

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// resolveJSONPath walks a simple JSONPath expression against root and returns
// the value at that path. Supported syntax:
//
//	$              the root
//	$.foo          map field "foo"
//	$.foo.bar      nested map access
//	$.foo[0]       array index
//	$.foo[0].bar   array element field access
//
// Anything more elaborate (filters, recursion, wildcards) is rejected with an
// error. We deliberately keep the language small so rules stay
// machine-inspectable for the dry-run UI.
func resolveJSONPath(expr string, root any) (any, error) {
	if expr == "" {
		return nil, errors.New("jsonpath: empty expression")
	}
	if expr == "$" {
		return root, nil
	}
	if !strings.HasPrefix(expr, "$") {
		return nil, fmt.Errorf("jsonpath: expression %q must start with '$'", expr)
	}

	tokens, err := tokenizeJSONPath(expr[1:])
	if err != nil {
		return nil, err
	}
	cur := root
	for _, tok := range tokens {
		next, terr := traverseToken(cur, tok)
		if terr != nil {
			return nil, terr
		}
		cur = next
	}
	return cur, nil
}

// jsonPathToken describes a single navigation step. For map access, key is
// non-empty. For index access, isIndex is true and idx holds the offset.
type jsonPathToken struct {
	key     string
	idx     int
	isIndex bool
}

func tokenizeJSONPath(rest string) ([]jsonPathToken, error) {
	var tokens []jsonPathToken
	for rest != "" {
		var (
			tok  jsonPathToken
			next string
			err  error
		)
		switch rest[0] {
		case '.':
			tok, next, err = consumeKey(rest)
		case '[':
			tok, next, err = consumeIndex(rest)
		default:
			return nil, fmt.Errorf("jsonpath: unexpected character %q", rest[0:1])
		}
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		rest = next
	}
	return tokens, nil
}

func consumeKey(rest string) (jsonPathToken, string, error) {
	rest = rest[1:] // skip '.'
	end := strings.IndexAny(rest, ".[")
	if end == -1 {
		if rest == "" {
			return jsonPathToken{}, "", errors.New("jsonpath: trailing '.'")
		}
		return jsonPathToken{key: rest}, "", nil
	}
	if end == 0 {
		return jsonPathToken{}, "", errors.New("jsonpath: empty key after '.'")
	}
	return jsonPathToken{key: rest[:end]}, rest[end:], nil
}

func consumeIndex(rest string) (jsonPathToken, string, error) {
	closing := strings.IndexByte(rest, ']')
	if closing == -1 {
		return jsonPathToken{}, "", errors.New("jsonpath: missing ']'")
	}
	body := rest[1:closing]
	n, err := strconv.Atoi(body)
	if err != nil {
		return jsonPathToken{}, "", fmt.Errorf("jsonpath: bracketed token %q is not a numeric index", body)
	}
	return jsonPathToken{idx: n, isIndex: true}, rest[closing+1:], nil
}

func traverseToken(cur any, tok jsonPathToken) (any, error) {
	if tok.isIndex {
		return traverseIndex(cur, tok.idx)
	}
	return traverseKey(cur, tok.key)
}

func traverseKey(cur any, key string) (any, error) {
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("jsonpath: cannot index non-object with key %q", key)
	}
	v, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("jsonpath: key %q not found", key)
	}
	return v, nil
}

func traverseIndex(cur any, idx int) (any, error) {
	arr, ok := cur.([]any)
	if !ok {
		return nil, fmt.Errorf("jsonpath: cannot index non-array with [%d]", idx)
	}
	if idx < 0 || idx >= len(arr) {
		return nil, fmt.Errorf("jsonpath: index %d out of range (len %d)", idx, len(arr))
	}
	return arr[idx], nil
}
