package connoauth

// ConfigSchemaPropertiesJSON declares the oauth_* keys this package parses, as
// JSON Schema properties for the kinds that delegate their OAuth to it.
//
// The keys stay top-level rather than nested under an "oauth" object because
// the platform's field encryptor walks only the top level of a connection
// config, which is what keeps oauth_client_secret encrypted at rest.
const ConfigSchemaPropertiesJSON = `{
  "oauth_grant": {
    "type": "string",
    "enum": ["client_credentials", "authorization_code", "jwt_bearer"],
    "description": "Which OAuth grant this connection uses (auth_mode=oauth)."
  },
  "oauth_token_url": {
    "type": "string",
    "description": "Token endpoint the grant is exchanged at."
  },
  "oauth_authorization_url": {
    "type": "string",
    "description": "Authorization endpoint a person is sent to (authorization_code grant)."
  },
  "oauth_client_id": {
    "type": "string",
    "description": "OAuth client identifier."
  },
  "oauth_client_secret": {
    "type": "string",
    "description": "OAuth client secret. Encrypted at rest; read back as \"[REDACTED]\", and sending that placeholder keeps the stored value."
  },
  "oauth_scope": {
    "type": "string",
    "description": "Space-separated scopes requested."
  },
  "oauth_prompt": {
    "type": "string",
    "description": "prompt parameter sent on the authorization request (for example consent or select_account)."
  },
  "oauth_endpoint_auth_style": {
    "type": "string",
    "description": "How the client credentials are presented at the token endpoint."
  }
}`
