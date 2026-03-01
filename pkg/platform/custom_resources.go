package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerCustomResources registers all user-defined static resources from config.
// Resources are registered regardless of resources.enabled (that gate applies only
// to the dynamic schema/glossary/availability templates).
func (p *Platform) registerCustomResources() {
	for i := range p.config.Resources.Custom {
		def := p.config.Resources.Custom[i] // copy for closure
		if err := validateCustomResourceDef(def); err != nil {
			slog.Warn("skipping invalid custom resource", "uri", def.URI, "error", err)
			continue
		}
		handler := func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return buildCustomResourceResult(def)
		}
		p.mcpServer.AddResource(&mcp.Resource{
			URI:         def.URI,
			Name:        def.Name,
			Description: def.Description,
			MIMEType:    def.MIMEType,
		}, handler)
		if p.resourceRegistry != nil {
			p.resourceRegistry[def.URI] = handler
		}
	}
}

// buildCustomResourceResult returns the resource contents for a custom resource definition.
// If ContentFile is set, the file is read fresh on every request (allows hot-reload).
func buildCustomResourceResult(def CustomResourceDef) (*mcp.ReadResourceResult, error) {
	content := def.Content
	if def.ContentFile != "" {
		// #nosec G304 -- ContentFile comes from admin-controlled YAML config
		data, err := os.ReadFile(def.ContentFile)
		if err != nil {
			return nil, fmt.Errorf("reading custom resource file %q: %w", def.ContentFile, err)
		}
		content = string(data)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      def.URI,
			MIMEType: def.MIMEType,
			Text:     content,
		}},
	}, nil
}

// validateCustomResourceDef checks that a CustomResourceDef is complete and unambiguous.
func validateCustomResourceDef(def CustomResourceDef) error {
	if def.URI == "" {
		return fmt.Errorf("uri is required")
	}
	if def.Name == "" {
		return fmt.Errorf("name is required")
	}
	if def.MIMEType == "" {
		return fmt.Errorf("mime_type is required")
	}
	if def.Content == "" && def.ContentFile == "" {
		return fmt.Errorf("one of content or content_file is required")
	}
	if def.Content != "" && def.ContentFile != "" {
		return fmt.Errorf("content and content_file are mutually exclusive")
	}
	return nil
}
