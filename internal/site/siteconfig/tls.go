package siteconfig

// TLSConfig is built-in certificate management.
type TLSConfig struct {
	ACME ACMEConfig `yaml:"acme"`
}

// ACMEConfig issues certificates for verified domains on a listener of its
// own.
type ACMEConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Address      string `yaml:"address"`
	Email        string `yaml:"email"`
	DirectoryURL string `yaml:"directory_url"`
	// CAFile trusts a private ACME server's root.
	CAFile string `yaml:"ca_file"`
}
