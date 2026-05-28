package registry

import (
	"testing"
)

func TestValidateConnectionConfig(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		cfg     map[string]any
		wantErr bool
	}{
		{
			name:    "trino valid",
			kind:    "trino",
			cfg:     map[string]any{"host": "trino.example.com"},
			wantErr: false,
		},
		{
			name:    "trino missing host",
			kind:    "trino",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "s3 empty config valid",
			kind:    "s3",
			cfg:     map[string]any{},
			wantErr: false,
		},
		{
			name:    "datahub missing url",
			kind:    "datahub",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "datahub valid",
			kind:    "datahub",
			cfg:     map[string]any{"url": "http://datahub.example.com"},
			wantErr: false,
		},
		{
			name:    "mcp gateway missing endpoint",
			kind:    "mcp",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "mcp gateway valid",
			kind:    "mcp",
			cfg:     map[string]any{"endpoint": "http://upstream.example.com/mcp"},
			wantErr: false,
		},
		{
			name:    "api gateway missing base_url",
			kind:    "api",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "api gateway valid",
			kind:    "api",
			cfg:     map[string]any{"base_url": "http://api.example.com"},
			wantErr: false,
		},
		{
			name:    "unknown kind passes",
			kind:    "custom",
			cfg:     map[string]any{},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConnectionConfig(tc.kind, tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateConnectionConfig(%q) error = %v, wantErr %v",
					tc.kind, err, tc.wantErr)
			}
		})
	}
}
