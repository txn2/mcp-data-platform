package portalcfg

import "testing"

func TestTitle(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		brand      string
		want       string
	}{
		{name: "configured title wins", configured: "ACME Analytics", brand: "ACME", want: "ACME Analytics"},
		{name: "configured title wins with no brand", configured: "ACME Analytics", want: "ACME Analytics"},
		{name: "no brand falls back to the product name", want: DefaultTitle},
		{name: "blank brand falls back to the product name", brand: "   ", want: DefaultTitle},
		{name: "brand gains the Portal suffix", brand: "ACME", want: "ACME Portal"},
		{name: "multi-word brand gains the suffix", brand: "ACME Data", want: "ACME Data Portal"},
		{name: "brand already ending in Portal is not doubled", brand: "ACME Portal", want: "ACME Portal"},
		{name: "suffix match ignores case", brand: "ACME portal", want: "ACME portal"},
		{name: "brand that is only the word Portal stands alone", brand: "Portal", want: "Portal"},
		{name: "surrounding space is trimmed", brand: "  ACME  ", want: "ACME Portal"},
		// The guard keys on the trailing word, never a bare substring, so a
		// brand that merely contains the letters still gains the suffix.
		{name: "suffix guard keys on the word not a substring", brand: "MyPortal", want: "MyPortal Portal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Title(tt.configured, tt.brand); got != tt.want {
				t.Errorf("Title(%q, %q) = %q, want %q", tt.configured, tt.brand, got, tt.want)
			}
		})
	}
}

func TestBrand(t *testing.T) {
	appConfig := map[string]any{
		"brand_name": "ACME",
		"brand_url":  "https://acme.example.com",
	}

	tests := []struct {
		name        string
		inName      string
		inURL       string
		appsEnabled bool
		appConfig   map[string]any
		wantName    string
		wantURL     string
	}{
		{
			name: "portal block wins", inName: "Contoso", inURL: "https://contoso.example.com",
			appsEnabled: true, appConfig: appConfig,
			wantName: "Contoso", wantURL: "https://contoso.example.com",
		},
		{
			name: "app config backfills both", appsEnabled: true, appConfig: appConfig,
			wantName: "ACME", wantURL: "https://acme.example.com",
		},
		{
			name: "app config backfills only the empty field", inName: "Contoso",
			appsEnabled: true, appConfig: appConfig,
			wantName: "Contoso", wantURL: "https://acme.example.com",
		},
		{
			name: "disabled apps contribute nothing", appsEnabled: false, appConfig: appConfig,
		},
		{
			name: "nil app config contributes nothing", appsEnabled: true,
		},
		{
			name: "non-string app values are ignored", appsEnabled: true,
			appConfig: map[string]any{"brand_name": 42, "brand_url": []string{"x"}},
		},
		{
			name: "values are trimmed", inName: "  Contoso  ", inURL: "  https://contoso.example.com  ",
			appsEnabled: true,
			wantName:    "Contoso", wantURL: "https://contoso.example.com",
		},
		{
			name: "backfilled values are trimmed", appsEnabled: true,
			appConfig: map[string]any{"brand_name": "  ACME  ", "brand_url": "  https://acme.example.com  "},
			wantName:  "ACME", wantURL: "https://acme.example.com",
		},
		{
			name: "whitespace-only portal values fall back", inName: "   ", inURL: "   ",
			appsEnabled: true, appConfig: appConfig,
			wantName: "ACME", wantURL: "https://acme.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotURL := Brand(tt.inName, tt.inURL, tt.appsEnabled, tt.appConfig)
			if gotName != tt.wantName || gotURL != tt.wantURL {
				t.Errorf("Brand(%q, %q, %v, %v) = (%q, %q), want (%q, %q)",
					tt.inName, tt.inURL, tt.appsEnabled, tt.appConfig, gotName, gotURL, tt.wantName, tt.wantURL)
			}
		})
	}
}

func TestMaxContentSize(t *testing.T) {
	if got := MaxContentSize(0); got != DefaultMaxContentSize {
		t.Errorf("MaxContentSize(0) = %d, want %d", got, DefaultMaxContentSize)
	}
	if got := MaxContentSize(4096); got != 4096 {
		t.Errorf("MaxContentSize(4096) = %d, want 4096", got)
	}
}

func TestS3Location(t *testing.T) {
	tests := []struct {
		name                   string
		bucket, prefix         string
		wantBucket, wantPrefix string
	}{
		{name: "both default", wantBucket: DefaultS3Bucket, wantPrefix: DefaultS3Prefix},
		{name: "bucket configured", bucket: "assets", wantBucket: "assets", wantPrefix: DefaultS3Prefix},
		{name: "prefix configured", prefix: "out/", wantBucket: DefaultS3Bucket, wantPrefix: "out/"},
		{name: "both configured", bucket: "assets", prefix: "out/", wantBucket: "assets", wantPrefix: "out/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBucket, gotPrefix := S3Location(tt.bucket, tt.prefix)
			if gotBucket != tt.wantBucket || gotPrefix != tt.wantPrefix {
				t.Errorf("S3Location(%q, %q) = (%q, %q), want (%q, %q)",
					tt.bucket, tt.prefix, gotBucket, gotPrefix, tt.wantBucket, tt.wantPrefix)
			}
		})
	}
}

func TestMaxVersionsError(t *testing.T) {
	ptr := func(n int) *int { return &n }
	tests := []struct {
		name       string
		configured *int
		wantErr    bool
	}{
		{name: "unset is the ordinary state"},
		{name: "zero keeps every version", configured: ptr(0)},
		{name: "a positive cap is accepted", configured: ptr(1)},
		{name: "the platform default is accepted", configured: ptr(100)},
		{name: "a negative cap is refused", configured: ptr(-1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaxVersionsError(tt.configured)
			if tt.wantErr && got == "" {
				t.Fatal("a negative retention default must be refused at startup")
			}
			if !tt.wantErr && got != "" {
				t.Fatalf("MaxVersionsError(%v) = %q, want no error", tt.configured, got)
			}
		})
	}
}
