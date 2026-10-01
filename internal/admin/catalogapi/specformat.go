package catalogapi

import (
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/soap"
	"github.com/txn2/mcp-data-platform/internal/swagger2"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// renderEffective fills in the OpenAPI document a spec entry serves.
//
// The gateway parses one format, and an operator supplies whichever one their
// upstream publishes. Reconciling the two is a save-time job rather than a
// load-time one for two reasons: a document is written once and read on every
// registration, every operations browse and every embedding pass, and a
// document that cannot be converted is an error the operator should see while
// they are looking at the form rather than a connection that silently
// registers with no operations.
//
// An OpenAPI spec clears OpenAPIContent rather than leaving whatever was
// there: changing a spec's format from wsdl back to openapi has to stop the
// stale render from being what Effective returns.
func renderEffective(entry *apicatalog.SpecEntry) error {
	switch entry.Format() {
	case apicatalog.FormatOpenAPI:
		return renderOpenAPI(entry)
	case apicatalog.FormatWSDL:
		_, rendered, err := soap.Import(entry.Content)
		if errors.Is(err, soap.ErrNotWSDL) {
			// The likeliest way to reach this is picking the wrong
			// format for a document that is otherwise fine, so the
			// refusal names the other one rather than only saying no.
			return fmt.Errorf("the content is not a WSDL 1.1 document; "+
				"set spec_format to openapi if it is an OpenAPI document: %w", err)
		}
		if err != nil {
			return fmt.Errorf("the WSDL could not be imported: %w", err)
		}
		entry.OpenAPIContent = rendered
		return nil
	case apicatalog.FormatGraphQL:
		// An SDL is rendered into nothing: it is served to a graphql
		// connection as the schema it answers with, not to the HTTP
		// gateway (#1745). Clearing OpenAPIContent is what stops a
		// spec changed from wsdl to graphql from serving the render the
		// change replaced.
		entry.OpenAPIContent = ""
		return nil
	default:
		return fmt.Errorf("spec_format %q is not one this platform reads: %w", entry.SpecFormat, apicatalog.ErrInvalidSpecFormat)
	}
}

// renderOpenAPI keeps an OpenAPI 3 document as it is, and converts a Swagger
// 2.0 one (#2005). Many internal services publish 2.0, because swaggo/swag,
// the usual generator for Go APIs, emits it; the operator saves what the
// service publishes and the platform converts it on every save and refresh,
// so no converted copy has to be kept in step with it.
//
// The 2.0 document stays as Content, so the editor round-trips what was
// supplied, and the conversion is OpenAPIContent, the split a WSDL has. Its
// basePath is the converted document's server path, which is the prefix the
// gateway uses when the spec's base_path is empty; an operator's base_path
// still overrides it, and a refresh follows a basePath the service changed.
func renderOpenAPI(entry *apicatalog.SpecEntry) error {
	entry.OpenAPIContent = ""
	if !swagger2.Is(entry.Content) {
		return nil
	}
	converted, err := swagger2.Convert(entry.Content)
	if err != nil {
		return fmt.Errorf("the Swagger 2.0 document could not be converted to OpenAPI 3: %w", err)
	}
	entry.OpenAPIContent = converted
	return nil
}

// prepareSpec renders a spec entry and stamps the operation count its
// effective document parses to.
//
// Every write path — inline, upload and refresh — does exactly this between
// building the entry and storing it, and doing it in one place is what keeps a
// WSDL refreshed from its URL going through the same conversion the first save
// did.
func prepareSpec(entry *apicatalog.SpecEntry) error {
	if err := renderEffective(entry); err != nil {
		return err
	}
	if !entry.ServesOpenAPI() {
		return prepareGraphQLSpec(entry)
	}
	if err := apicatalog.ValidateContent(entry.Effective()); err != nil {
		return fmt.Errorf("the effective OpenAPI document is not valid: %w", err)
	}
	entry.OperationCount = apicatalog.CountOperations(entry.Effective())
	return nil
}

// prepareGraphQLSpec validates a GraphQL spec entry's SDL and stamps the
// operations it exposes.
//
// The count is what the embedding reconciler compares against the rows in
// api_catalog_operation_embeddings, so it is the same walk the embedding
// pass and the operations browser make: the namespace descent at the
// default depth. All three agree, so the reconciler never sees a gap it
// cannot close.
//
// A connection that sets a deeper namespace_depth indexes operations the
// catalog did not count and therefore did not embed. Those operations are
// listed and invoked as any other, and rank lexically until the catalog is
// walked that deep; they are not a gap the reconciler chases, because the
// count and the row it compares it against were both taken here.
//
// An introspection result is refused rather than accepted, because what a
// catalog stores is the document an operator can read back and diff, and
// graphql_export writes SDL.
func prepareGraphQLSpec(entry *apicatalog.SpecEntry) error {
	schema, err := gqlschema.Load(entry.Content)
	if err != nil {
		return fmt.Errorf("the content is not a GraphQL schema in SDL; "+
			"graphql_export and a schema registry both write SDL: %w", err)
	}
	entry.OperationCount = len(gqlschema.Operations(schema, gqlschema.DefaultNamespaceDepth))
	return nil
}
