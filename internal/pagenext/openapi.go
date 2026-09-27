package pagenext

import (
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
)

// FromOpenAPI reads the query parameters of an OpenAPI operation -- its
// path item's and its own, in that order -- with their declared defaults
// and minimums.
func FromOpenAPI(lists ...openapi3.Parameters) []Param {
	var out []Param
	for _, list := range lists {
		for _, ref := range list {
			if ref == nil || ref.Value == nil || ref.Value.In != openapi3.ParameterInQuery {
				continue
			}
			p := Param{Name: ref.Value.Name}
			if s := ref.Value.Schema; s != nil && s.Value != nil {
				if s.Value.Default != nil {
					p.Default = fmt.Sprint(s.Value.Default)
				}
				p.Minimum = s.Value.Min
			}
			out = append(out, p)
		}
	}
	return out
}
