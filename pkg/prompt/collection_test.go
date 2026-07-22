package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateCollectionName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"valid", "Sales Reporting", ""},
		{"empty", "", "required"},
		{"whitespace only", "   ", "required"},
		{"max length", strings.Repeat("a", 128), ""},
		{"too long", strings.Repeat("a", 129), "at most 128"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCollectionName(tt.input)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// capableStore implements Store plus CollectionStore.
type capableStore struct {
	Store
	CollectionStore
}

// providerStore implements Store plus CollectionProvider, the decorator shape.
type providerStore struct {
	Store
	inner CollectionStore
}

func (p providerStore) Collections() CollectionStore { return p.inner }

// bareStore implements only Store.
type bareStore struct{ Store }

// markerCollections is a distinguishable CollectionStore value.
type markerCollections struct{ CollectionStore }

func TestAsCollectionStore(t *testing.T) {
	marker := markerCollections{}

	direct := capableStore{CollectionStore: marker}
	assert.NotNil(t, AsCollectionStore(direct), "direct implementation resolves")

	viaProvider := providerStore{inner: marker}
	assert.Equal(t, CollectionStore(marker), AsCollectionStore(viaProvider), "provider delegates to the wrapped capability")

	emptyProvider := providerStore{inner: nil}
	assert.Nil(t, AsCollectionStore(emptyProvider), "provider over an incapable base resolves to nil")

	assert.Nil(t, AsCollectionStore(bareStore{}), "no capability, no provider: nil")
}
