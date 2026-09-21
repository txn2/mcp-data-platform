package s3

// ConfigSchemaJSON declares what an s3 connection's config takes. See the same
// constant on the trino kind for why a kind declares this at all (#1805).
const ConfigSchemaJSON = `{
  "type": "object",
  "properties": {
    "region": {"type": "string", "description": "AWS region the bucket lives in. Most S3-compatible stores accept any value; AWS does not."},
    "endpoint": {"type": "string", "description": "S3 API endpoint. Omit for AWS itself; set it for MinIO, SeaweedFS or another S3-compatible store."},
    "public_endpoint": {"type": "string", "description": "Endpoint used when building a link a person's browser will follow, where that differs from the one the platform calls."},
    "access_key_id": {"type": "string", "description": "Access key. Omit to use the ambient credentials or a named profile."},
    "secret_access_key": {"type": "string", "description": "Secret key. Encrypted at rest; read back as \"[REDACTED]\", and sending that placeholder keeps the stored value."},
    "session_token": {"type": "string", "description": "Session token for temporary credentials. Encrypted at rest."},
    "profile": {"type": "string", "description": "Named profile in the ambient AWS credentials file."},
    "bucket_prefix": {"type": "string", "description": "Restricts this connection to buckets whose name starts with this prefix."},
    "read_only": {"type": "boolean", "description": "Refuse writes on this connection."},
    "use_path_style": {"type": "boolean", "description": "Address buckets as a path rather than a subdomain, which most S3-compatible stores require."},
    "disable_ssl": {"type": "boolean", "description": "Speak plain HTTP to the endpoint."},
    "max_get_size": {"type": "integer", "description": "Ceiling on the bytes one read may return."},
    "max_put_size": {"type": "integer", "description": "Ceiling on the bytes one write may send."},
    "timeout": {"type": ["string", "integer"], "description": "Request timeout, as a duration string or a number of seconds."},
    "connection_name": {"type": "string", "description": "The name calls bind this connection by, when it differs from the instance name it is stored under."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it."}
  }
}`
