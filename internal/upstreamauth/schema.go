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
    "enum": ["none", "bearer", "api_key", "basic", "oauth", "mtls", "signed_jwt", "hmac", "session_login"],
    "description": "How the platform authenticates to the upstream. Defaults to none."
  },
  "credential": {
    "type": "string",
    "description": "The bearer token (auth_mode=bearer), the API key (auth_mode=api_key), or the signing secret (auth_mode=hmac). Encrypted at rest; read back as \"[REDACTED]\", and sending that placeholder keeps the stored value."
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
  "trace_propagation": {
    "type": "boolean",
    "description": "Whether every request to the upstream carries the W3C traceparent and tracestate headers, so the upstream's own telemetry joins the caller's trace. Defaults to true; set false for an upstream that rejects unknown headers or must not see the deployment's trace ids."
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
  "jwt_subject": {"type": "string", "description": "sub claim on the signed JWT. With a Google service account, the Workspace user it impersonates under domain-wide delegation; unset, the account acts as itself."},
  "jwt_audience": {"type": "string", "description": "aud claim on the signed JWT."},
  "jwt_token_lifetime": {
    "type": ["string", "integer"],
    "description": "How long a minted JWT is valid, as a duration string or seconds."
  },
  "google_service_account_json": {
    "type": "string",
    "description": "A Google service account's JSON key file, whole, as Google Cloud issues it. Sets auth_mode oauth and oauth_grant jwt_bearer, and fills the signing key, key id, issuer and token endpoint from the file; set oauth_scope, and jwt_subject only to impersonate a Workspace user under domain-wide delegation. An explicit jwt_* or oauth_* key wins over the file. Encrypted at rest and read back as \"[REDACTED]\", beside google_service_account_identity (client_email, project_id, private_key_id)."
  },
  "google_service_account_secret": {
    "type": "string",
    "description": "The name of a stored secret holding a Google service account's key file, in place of google_service_account_json, so the key never travels in the connection's config. The secret's allow_connections must list this connection and it must set no allow_personas. It is read at every token exchange, so a key rotated in the secret is used from the next token on. The token endpoint defaults to https://oauth2.googleapis.com/token."
  },
  "oauth_scope_placement": {
    "type": "string",
    "enum": ["param", "claim", "both"],
    "description": "Where oauth_grant=jwt_bearer carries oauth_scope: the scope form parameter (param, the default), a scope claim in the signed assertion (claim, which Google reads, and the default with a Google service account), or both."
  },
  "path_secret": {
    "type": "string",
    "description": "Appended to every request's path as it is sent, for a receiver that authenticates by a secret in the URL (a chat incoming webhook's token, an inbound webhook source's path_token). Encrypted at rest and read back as \"[REDACTED]\"; unlike base_url it never appears in a connection read, a call's path or an error."
  },
  "hmac_preset": {
    "type": "string",
    "enum": ["standard_webhooks", "github", "stripe", "platform"],
    "description": "A receiver's whole signing convention in one setting (auth_mode=hmac). A key set on the connection overrides the preset's value for it."
  },
  "hmac_algorithm": {"type": "string", "enum": ["sha256", "sha1", "sha512"], "description": "HMAC algorithm (auth_mode=hmac). Defaults to sha256."},
  "hmac_encoding": {"type": "string", "enum": ["hex", "base64"], "description": "How the signature is written (auth_mode=hmac). Defaults to hex."},
  "hmac_signature_header": {"type": "string", "description": "Header the signature is written to (auth_mode=hmac). Defaults to X-Signature."},
  "hmac_prefix": {"type": "string", "description": "Written before the signature, such as sha256= or v1, (auth_mode=hmac)."},
  "hmac_signed": {
    "type": "string",
    "enum": ["body", "timestamp.body", "id.timestamp.body"],
    "description": "What is signed (auth_mode=hmac): the body; the timestamp, a dot and the body; or the delivery id, a dot, the timestamp, a dot and the body (Standard Webhooks). Defaults to body."
  },
  "hmac_header_format": {
    "type": "string",
    "enum": ["", "stripe"],
    "description": "stripe writes the signature header as t=<timestamp>,v1=<signature> and signs the timestamp and the body (auth_mode=hmac)."
  },
  "hmac_timestamp_header": {"type": "string", "description": "Header the timestamp is written to; required when hmac_signed includes the timestamp (auth_mode=hmac)."},
  "hmac_timestamp_unit": {"type": "string", "enum": ["seconds", "milliseconds"], "description": "Unit of the Unix timestamp (auth_mode=hmac). Defaults to seconds."},
  "hmac_id_header": {"type": "string", "description": "Header carrying the delivery id; required for id.timestamp.body. A call that sets this header chooses the id, otherwise one is generated per request (auth_mode=hmac)."},
  "session_login_url": {"type": "string", "description": "Sign-in endpoint (auth_mode=session_login): an absolute URL, or a path resolved against the connection's base URL."},
  "session_login_method": {"type": "string", "description": "Sign-in request method (auth_mode=session_login). Defaults to POST."},
  "session_login_body": {
    "type": "string",
    "description": "Sign-in request body (auth_mode=session_login), with {{secret}} where session_login_secret is written. Not secret itself, so it reads back as written; the secret is escaped for session_login_content_type."
  },
  "session_login_content_type": {"type": "string", "description": "Media type of the sign-in body (auth_mode=session_login): application/json (default), application/xml or application/x-www-form-urlencoded."},
  "session_login_secret": {"type": "string", "description": "The credential written into session_login_body at {{secret}} (auth_mode=session_login): a personal access token secret, an API key or a password. Encrypted at rest and read back as \"[REDACTED]\"."},
  "session_login_headers": {
    "type": "object",
    "additionalProperties": {"type": "string"},
    "description": "Headers sent on the sign-in request only (auth_mode=session_login). The sign-in asks for JSON with Accept: application/json unless this sets another."
  },
  "session_token_source": {"type": "string", "description": "Where the session token is in the sign-in response (auth_mode=session_login): body:<dotted json path>, such as body:credentials.token, or header:<name>, such as header:X-MSTR-AuthToken."},
  "session_token_header": {"type": "string", "description": "Header the session token is sent in on every call (auth_mode=session_login), such as X-Tableau-Auth. With this and session_token_prefix both blank the token is sent as Authorization: Bearer <token>."},
  "session_token_prefix": {"type": "string", "description": "Written before the session token in session_token_header (auth_mode=session_login)."},
  "session_ttl": {"type": ["string", "integer"], "description": "How long a session is used before the platform signs in again without waiting to be rejected (auth_mode=session_login), as a duration string or seconds. Unset uses a session until the upstream rejects it."},
  "session_logout_url": {"type": "string", "description": "Sign-out endpoint, called with the session token when the connection is removed, replaced or shut down (auth_mode=session_login)."},
  "session_logout_method": {"type": "string", "description": "Sign-out request method (auth_mode=session_login). Defaults to POST."},
  "session_capture": {
    "type": "object",
    "additionalProperties": {"type": "string"},
    "description": "Further values the sign-in response carries, by name, each read from a source in session_token_source's form (auth_mode=session_login). A call writes {session.<name>} in its path to have the value put there, such as site_id: body:credentials.site.id for /api/3.22/sites/{session.site_id}/workbooks."
  },
  "session_expired_statuses": {
    "type": ["array", "string"],
    "items": {"type": "integer"},
    "description": "Statuses that mean the session is no longer accepted, answered by signing in again and replaying the call once (auth_mode=session_login). Defaults to [401]."
  },
  "session_expired_marker": {"type": "string", "description": "Text whose presence in a response body means the session is no longer accepted, for an upstream that answers an expired session with 200 or 403 and an error body (auth_mode=session_login)."},
  "jwt_issued_at_skew": {
    "type": ["string", "integer"],
    "description": "How far back to date the iat claim, for an upstream with a fast clock."
  }
}`
