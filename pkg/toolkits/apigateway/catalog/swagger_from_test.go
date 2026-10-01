package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/swagger2"
)

func TestConvertedFrom(t *testing.T) {
	assert.Equal(t, swagger2.ConvertedFrom, SpecEntry{OpenAPIContent: "{}"}.ConvertedFrom())
	assert.Empty(t, SpecEntry{}.ConvertedFrom())
	assert.Empty(t, SpecEntry{SpecFormat: FormatWSDL, OpenAPIContent: "{}"}.ConvertedFrom(), "a WSDL render is not a conversion from 2.0")
}
