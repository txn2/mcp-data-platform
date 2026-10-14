package catalogimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/outbound"
)

// graphQLPath is where DataHub's GMS answers GraphQL.
const graphQLPath = "/api/graphql"

// requestTimeout bounds one request the import makes to DataHub.
const requestTimeout = 60 * time.Second

// maxResponseBytes bounds one GraphQL answer read from DataHub.
const maxResponseBytes = 64 << 20

// maxContextDocuments is how many context documents are read per entity.
const maxContextDocuments = 100

// Client reads DataHub's GraphQL API with a personal access token.
type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

// NewClient reads the DataHub at address, the GMS address with or without
// /api/graphql, with token.
func NewClient(address, token string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("the address %q is not DataHub's http or https address", address)
	}
	base := strings.TrimSuffix(u.String(), "/")
	endpoint := base
	if !strings.HasSuffix(base, graphQLPath) {
		endpoint = base + graphQLPath
	}
	return &Client{
		endpoint: endpoint,
		token:    token,
		http:     outbound.NewClient(outbound.Options{Kind: outbound.KindImport, Timeout: requestTimeout}),
	}, nil
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Execute runs one GraphQL query and decodes its data into result. A GraphQL
// error, an answer with no data, and any status but 200 are errors.
func (c *Client) Execute(ctx context.Context, query string, variables map[string]any, result any) error {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("encoding the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req) //nolint:gosec // G704: the address is the operator's DataHub, from configuration or an administrator
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}
	if err := statusError(resp.StatusCode, raw); err != nil {
		return err
	}
	return decodeAnswer(raw, result)
}

// statusError reads a status other than 200 as an error naming the remedy.
func statusError(status int, body []byte) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("request failed: %d %s: the token is not a DataHub personal access token that can read the catalog",
			status, http.StatusText(status))
	default:
		return fmt.Errorf("request failed: %d %s: %s", status, http.StatusText(status), preview(body))
	}
}

// decodeAnswer decodes a GraphQL answer's data into result.
func decodeAnswer(raw []byte, result any) error {
	var resp graphQLResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("the answer is not GraphQL: %w: %s", err, preview(raw))
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("the query was refused: %s", strings.Join(msgs, "; "))
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return errors.New("the answer holds no data")
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Data, result); err != nil {
		return fmt.Errorf("decoding the answer: %w", err)
	}
	return nil
}

// preview is the start of a body, for an error.
func preview(b []byte) string {
	const n = 200
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ContextDocument is a markdown note DataHub keeps on an entity.
type ContextDocument struct {
	ID       string
	Title    string
	Content  string
	Category string
}

// contextDocumentsQuery reads the context documents of a dataset.
const contextDocumentsQuery = `query importContextDocuments($urn: String!, $input: RelatedDocumentsInput!) {
  entity(urn: $urn) {
    ... on Dataset {
      relatedDocuments(input: $input) {
        documents { urn subType info { title contents { text } } }
      }
    }
  }
}`

type contextDocumentsResponse struct {
	Entity struct {
		RelatedDocuments *struct {
			Documents []struct {
				URN     string `json:"urn"`
				SubType string `json:"subType"`
				Info    struct {
					Title    string `json:"title"`
					Contents struct {
						Text string `json:"text"`
					} `json:"contents"`
				} `json:"info"`
			} `json:"documents"`
		} `json:"relatedDocuments"`
	} `json:"entity"`
}

// GetContextDocuments reads the context documents DataHub keeps on a dataset,
// at most maxContextDocuments of them.
func (c *Client) GetContextDocuments(ctx context.Context, urn string) ([]ContextDocument, error) {
	var resp contextDocumentsResponse
	vars := map[string]any{"urn": urn, "input": map[string]any{"start": 0, "count": maxContextDocuments}}
	if err := c.Execute(ctx, contextDocumentsQuery, vars, &resp); err != nil {
		if strings.Contains(err.Error(), "relatedDocuments") {
			return nil, nil // a DataHub release before context documents has none to read
		}
		return nil, err
	}
	if resp.Entity.RelatedDocuments == nil {
		return nil, nil
	}
	out := make([]ContextDocument, 0, len(resp.Entity.RelatedDocuments.Documents))
	for _, d := range resp.Entity.RelatedDocuments.Documents {
		out = append(out, ContextDocument{
			ID:       strings.TrimPrefix(d.URN, "urn:li:document:"),
			Title:    d.Info.Title,
			Content:  d.Info.Contents.Text,
			Category: d.SubType,
		})
	}
	return out, nil
}

// Open reads the DataHub at address with token: the SourceFactory a runner
// is built with.
func Open(address, token string) (Source, error) {
	c, err := NewClient(address, token)
	if err != nil {
		return nil, err
	}
	return NewDataHub(c), nil
}
