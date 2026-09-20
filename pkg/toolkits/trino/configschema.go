package trino

// ConfigSchemaJSON declares what a trino connection's config takes.
//
// A connection stored through the admin API is a freeform object on the wire,
// so until this existed the only way to learn a key's name was to read a
// deployment's configuration file — which an operator with no cluster access
// cannot do, and which is how a connection came to be created with values that
// could never work (#1805). It is a declaration, not a gate: a key not named
// here is stored and ignored, which the discovery endpoint says plainly.
const ConfigSchemaJSON = `{
  "type": "object",
  "required": ["host"],
  "properties": {
    "host": {"type": "string", "description": "Trino coordinator hostname. Required."},
    "port": {"type": "integer", "description": "Coordinator port. Defaults to 8080 plain, and a TLS deployment is usually 443."},
    "user": {"type": "string", "description": "Trino user the connection authenticates and is audited as."},
    "password": {"type": "string", "description": "Password for that user. Encrypted at rest; read back as \"[REDACTED]\", and sending that placeholder keeps the stored value."},
    "catalog": {"type": "string", "description": "Catalog a statement uses when it names none. A DEFAULT, not a boundary: a fully qualified statement reaches any catalog this connection's Trino identity may reach."},
    "schema": {"type": "string", "description": "Schema a statement uses when it names none. A default, on the same terms as catalog."},
    "ssl": {"type": "boolean", "description": "Speak HTTPS to the coordinator. Absent means plain HTTP."},
    "ssl_verify": {"type": "boolean", "description": "Verify the coordinator's certificate. Defaults to true."},
    "read_only": {"type": "boolean", "description": "Refuse write-class statements on this connection. Deployment-wide: it is a statement check, not a scope, so read_only: false permits writes wherever this connection's Trino identity may write."},
    "timeout": {"type": ["string", "integer"], "description": "Query timeout, as a duration string (\"30s\") or a number of seconds."},
    "default_limit": {"type": "integer", "description": "Row limit applied to a query that sets none."},
    "max_limit": {"type": "integer", "description": "Ceiling on the row limit a query may ask for."},
    "connection_name": {"type": "string", "description": "The name calls bind this connection by, when it differs from the instance name it is stored under."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it."},
    "progress_enabled": {"type": "boolean", "description": "Send query-progress notifications for queries on this connection."},
    "scratch": {
      "type": "object",
      "description": "Where a table registration writes on this connection. A target, not a boundary.",
      "properties": {
        "catalog": {"type": "string", "description": "Catalog a registration creates its table in."},
        "schema": {"type": "string", "description": "Schema a registration creates its table in."}
      }
    },
    "elicitation": {"type": "object", "description": "Per-connection elicitation triggers (cost estimation, PII consent)."}
  }
}`
