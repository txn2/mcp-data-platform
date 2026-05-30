package apigateway

import "fmt"

// OperationItem is one embeddable operation extracted from a spec:
// the synthesized operation id and the text fed to the embedding
// provider for semantic ranking. It is the api-catalog unit the
// indexjobs framework embeds; the platform's api-catalog Source
// maps each one to an indexjobs.Item.
type OperationItem struct {
	// OperationID is the synthesized id buildOperationIndex assigns
	// to each path/method pair (the spec's operationId when set,
	// "METHOD path" otherwise). It is the dedup key vectors are
	// stored against.
	OperationID string

	// Text is the per-operation text semantic ranking embeds.
	Text string
}

// BuildOperationItems parses content as an OpenAPI document and
// returns one OperationItem per operation, in stable (path, method)
// order. The embed text is built with an empty base path so a
// per-spec base_path change does not invalidate every vector.
//
// It is the api-catalog side of the indexjobs Source contract:
// content in, (operation id, embed text) pairs out. The framework
// owns everything downstream (text-hash dedup, batched provider
// calls, persistence). Returns an empty slice (nil error) when the
// spec parses to zero operations; an error only on a parse failure.
func BuildOperationItems(content, specName string) ([]OperationItem, error) {
	doc, err := parseOpenAPISpec(content)
	if err != nil {
		return nil, fmt.Errorf("build operation items: %w", err)
	}
	ops, texts := buildOperationIndex(doc, specName, "")
	if len(ops) == 0 {
		return nil, nil
	}
	out := make([]OperationItem, len(ops))
	for i, op := range ops {
		out[i] = OperationItem{OperationID: op.OperationID, Text: texts[i]}
	}
	return out, nil
}
