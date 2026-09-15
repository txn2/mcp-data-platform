package catalogapi

import (
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/soap"
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
		entry.OpenAPIContent = ""
		return nil
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
	default:
		return fmt.Errorf("spec_format %q is not one this platform reads: %w", entry.SpecFormat, apicatalog.ErrInvalidSpecFormat)
	}
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
	if err := apicatalog.ValidateContent(entry.Effective()); err != nil {
		return fmt.Errorf("the effective OpenAPI document is not valid: %w", err)
	}
	entry.OperationCount = apicatalog.CountOperations(entry.Effective())
	return nil
}
