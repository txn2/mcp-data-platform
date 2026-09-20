package upstreamauth

// ConfigSchemaPropertiesJSON declares the keys every HTTP-based connection kind
// reads through this package, as JSON Schema properties to be spliced into that
// kind's own config schema.
//
// It is here, beside the constants the parser reads, because the whole reason
// this package exists is that two kinds must not fork the rules; a schema
// written per kind would fork the description of them. TestConfigSchemaDeclares
// EveryKey holds this fragment against those constants, so a key added to the
// parser and not to this text fails the build's tests rather than going
// undocumented.
const ConfigSchemaPropertiesJSON = `{
  "auth_mode": {
    "type": "string",
    "enum": ["none", "bearer", "api_key", "basic", "oauth", "mtls", "signed_jwt"],
    "description": "How the platform authenticates to the upstream. Defaults to none."
  },
  "credential": {
    "type": "string",
    "description": "The bearer token (auth_mode=bearer) or the API key (auth_mode=api_key). Encrypted at rest; read back as \"[REDACTED]\", and sending that placeholder keeps the stored value."
  },
  "api_key_header": {
    "type": "string",
    "description": "Header the API key is sent in (auth_mode=api_key). Defaults to X-API-Key."
  },
  "api_key_param": {
    "type": "string",
    "description": "Query parameter the API key is sent in, when api_key_placement is query."
  },
  "api_key_placement": {
    "type": "string",
    "enum": ["header", "query"],
    "description": "Whether the API key rides in a header or a query parameter. Defaults to header."
  },
  "username": {
    "type": "string",
    "description": "Username for auth_mode=basic."
  },
  "password": {
    "type": "string",
    "description": "Password for auth_mode=basic. Encrypted at rest and read back as \"[REDACTED]\"."
  },
  "connect_timeout": {
    "type": ["string", "integer"],
    "description": "How long to wait for the connection to open, as a duration string (\"10s\") or a number of seconds."
  },
  "call_timeout": {
    "type": ["string", "integer"],
    "description": "How long one call may take, as a duration string (\"30s\") or a number of seconds."
  },
  "max_response_bytes": {
    "type": "integer",
    "description": "Ceiling on how much of a response is read."
  },
  "static_headers": {
    "type": "object",
    "additionalProperties": {"type": "string"},
    "description": "Operator-owned headers appended to every outbound request, for an upstream that wants a subscription or routing header beside its credential. Values are encrypted at rest. A model may not set these and may not override them per call."
  },
  "mtls_client_cert_pem": {
    "type": "string",
    "description": "PEM client certificate for auth_mode=mtls."
  },
  "mtls_client_key_pem": {
    "type": "string",
    "description": "PEM private key for auth_mode=mtls. Encrypted at rest and read back as \"[REDACTED]\"."
  },
  "tls_ca_bundle_pem": {
    "type": "string",
    "description": "PEM bundle of the certificate authorities this connection's TLS is verified against."
  },
  "identity_passthrough": {
    "type": "boolean",
    "description": "Forward the calling person's own bearer token to the upstream in place of a shared credential, so the call acts as them."
  },
  "jwt_algorithm": {
    "type": "string",
    "description": "Signing algorithm for auth_mode=signed_jwt."
  },
  "jwt_client_secret": {
    "type": "string",
    "description": "HMAC secret for a symmetric signed_jwt algorithm. Encrypted at rest."
  },
  "jwt_private_key_pem": {
    "type": "string",
    "description": "PEM private key for an asymmetric signed_jwt algorithm. Encrypted at rest."
  },
  "jwt_key_id": {"type": "string", "description": "kid header on the signed JWT."},
  "jwt_issuer": {"type": "string", "description": "iss claim on the signed JWT."},
  "jwt_subject": {"type": "string", "description": "sub claim on the signed JWT."},
  "jwt_audience": {"type": "string", "description": "aud claim on the signed JWT."},
  "jwt_token_lifetime": {
    "type": ["string", "integer"],
    "description": "How long a minted JWT is valid, as a duration string or seconds."
  },
  "jwt_issued_at_skew": {
    "type": ["string", "integer"],
    "description": "How far back to date the iat claim, for an upstream with a fast clock."
  }
}`
