package portal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// manage_resource extract from a bucket prefix (#2593): the objects under a
// prefix of a bucket on an S3 connection written out as managed resources of a
// folder. The caller's access to the connection and the bucket, and the threat
// model, are the extractor's (internal/bucketsource); this file is the tool's
// half: the arguments and the answer.

// BucketSource names the objects under a prefix of a bucket on a connection.
type BucketSource struct {
	Connection string `json:"connection"`
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix,omitempty"`
}

// PrefixExtraction is what manage_resource extract with from_bucket asks of the
// extractor: an archive extraction whose members are the objects under a
// prefix.
type PrefixExtraction struct {
	Source        BucketSource
	Scope         string
	ScopeID       string
	Path          string
	Replace       bool
	Description   string
	Tags          []string
	ChangeSummary string
	Index         string
}

// PrefixExtractor writes the objects under a bucket prefix out as managed
// resources. A failure part way through returns what was written before it.
type PrefixExtractor interface {
	ExtractPrefix(ctx context.Context, req PrefixExtraction, claims resource.Claims) (*ExtractedArchive, error)
}

// SetPrefixExtractor binds the extractor behind extract with from_bucket.
// Without it the action reports that the deployment cannot read a bucket.
func (t *Toolkit) SetPrefixExtractor(x PrefixExtractor) {
	t.prefixExtractor = x
}

// prefixExtractOutput is what an extraction from a bucket prefix reports.
type prefixExtractOutput struct {
	// Source is the bucket prefix read: s3://bucket/prefix on connection.
	Source  string            `json:"source"`
	Members []ExtractedMember `json:"members"`
	Count   int               `json:"count"`
	Message string            `json:"message"`
}

func (t *Toolkit) handleExtractPrefix(ctx context.Context, input manageResourceInput) (*mcp.CallToolResult, any, error) {
	if t.prefixExtractor == nil {
		return toolkit.ErrorResult("This deployment cannot extract from a bucket: it has no S3 connections or no " +
			"managed-resource library to write into. Nothing was written."), nil, nil
	}
	req, err := prefixExtractionOf(input)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	out, err := t.prefixExtractor.ExtractPrefix(ctx, req, refClaims(ctx))
	if err != nil {
		return toolkit.ErrorResult(extractFailure(err, out)), nil, nil
	}
	source := fmt.Sprintf("s3://%s/%s on connection %s", req.Source.Bucket, req.Source.Prefix, req.Source.Connection)
	return toolkit.JSONResultTyped(prefixExtractOutput{
		Source: source, Members: out.Members, Count: len(out.Members),
		Message: strings.Replace(extractMessage(out), "from the "+out.Format+" archive", "from "+source, 1),
	})
}

// prefixExtractionOf validates the arguments an extract from a bucket needs.
func prefixExtractionOf(input manageResourceInput) (PrefixExtraction, error) {
	if strings.TrimSpace(input.Reference) != "" {
		return PrefixExtraction{}, errors.New("extract reads an archive (reference) or a bucket prefix (from_bucket), not both")
	}
	if strings.TrimSpace(input.Members) != "" || strings.TrimSpace(input.Filename) != "" {
		return PrefixExtraction{}, errors.New("members and filename choose files of an archive; to extract part of a bucket, " +
			"name a narrower prefix in from_bucket")
	}
	if strings.TrimSpace(input.Path) == "" {
		return PrefixExtraction{}, errors.New("path is required for extract: it is the folder the objects are filed " +
			"under; an object's own folders under the prefix are created beneath it")
	}
	if err := validateIfExists(input.IfExists); err != nil && !replaceIfExists(input.IfExists) {
		return PrefixExtraction{}, err
	}
	if err := validateIndex(input.Index); err != nil {
		return PrefixExtraction{}, err
	}
	return PrefixExtraction{
		Source: *input.FromBucket,
		Scope:  input.Scope, ScopeID: input.ScopeID, Path: input.Path,
		Replace:       replaceIfExists(input.IfExists),
		Description:   input.Description,
		Tags:          normalizeTags(input.Tags),
		ChangeSummary: input.ChangeSummary,
		Index:         input.Index,
	}, nil
}
