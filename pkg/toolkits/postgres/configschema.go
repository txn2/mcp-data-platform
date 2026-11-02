package postgres

// ConfigSchemaJSON declares what a postgres connection's config takes. See the
// same constant on the trino kind for why a kind declares this at all (#1805).
const ConfigSchemaJSON = `{
  "type": "object",
  "required": ["host", "database", "user"],
  "properties": {
    "host": {"type": "string", "description": "Host name or address of the PostgreSQL server. Required."},
    "port": {"type": "integer", "description": "Port of the server. Defaults to 5432."},
    "database": {"type": "string", "description": "Database to connect to. Required. The platform's own database is refused."},
    "user": {"type": "string", "description": "Database user every call on this connection runs as. Required. What the tools can do is what this user's grants allow."},
    "password": {"type": "string", "description": "The user's password, or a {{secret:<name>}} reference to a stored secret. Encrypted at rest and never returned by a read."},
    "sslmode": {"type": "string", "enum": ["disable", "require", "verify-ca", "verify-full"], "description": "TLS mode, as libpq names it. Defaults to verify-full: the server's certificate is checked against the system roots and tls_ca_bundle_pem, and its name against host."},
    "tls_ca_bundle_pem": {"type": "string", "description": "PEM certificates trusted beside the system roots when verifying the server."},
    "mtls_client_cert_pem": {"type": "string", "description": "PEM client certificate presented to the server, with mtls_client_key_pem."},
    "mtls_client_key_pem": {"type": "string", "description": "PEM private key of the client certificate. Encrypted at rest and never returned by a read."},
    "max_conns": {"type": "integer", "description": "Pool size on each replica. Defaults to 5, at most 100."},
    "statement_timeout": {"type": "string", "description": "Longest one statement may run, as a Go duration (\"30s\"). Defaults to 30s, at most 10m. A call's timeout_seconds may only lower it."},
    "max_rows": {"type": "integer", "description": "Most rows one result returns. Defaults to 1000, at most 100000."},
    "read_only": {"type": "boolean", "description": "Refuse platform.execute on this connection, for every script."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it."}
  }
}`
