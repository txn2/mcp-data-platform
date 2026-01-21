# API Key Authentication

API keys provide simple authentication for service accounts, automation, and development environments. Each key is associated with a name and a set of roles.

## Configuration

```yaml
auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: "admin-service"
        roles: ["admin"]

      - key: ${API_KEY_ANALYST}
        name: "analyst-service"
        roles: ["analyst"]

      - key: ${API_KEY_READONLY}
        name: "readonly"
        roles: ["viewer"]
```

| Field | Required | Description |
|-------|----------|-------------|
| `enabled` | Yes | Enable API key authentication |
| `keys` | Yes | List of API key definitions |
| `keys[].key` | Yes | The API key value (use env vars) |
| `keys[].name` | Yes | Identifier for this key |
| `keys[].roles` | Yes | Roles assigned to this key |

## Using API Keys

Include the API key in the Authorization header:

```
Authorization: Bearer <api-key>
```

Or as a query parameter (for SSE connections that don't support headers):

```
GET /sse?api_key=<api-key>
```

## Key Generation

Generate secure API keys using standard tools:

```bash
# Using OpenSSL
openssl rand -base64 32

# Using Python
python3 -c "import secrets; print(secrets.token_urlsafe(32))"

# Using uuidgen
uuidgen | tr -d '-'
```

Store keys in environment variables, not in configuration files:

```bash
export API_KEY_ADMIN="your-secure-key-here"
export API_KEY_ANALYST="another-secure-key"
```

## Role Assignment

Each API key maps directly to roles:

```yaml
keys:
  - key: ${API_KEY_DATA_TEAM}
    name: "data-team"
    roles: ["analyst", "data_engineer"]
```

These roles are used for persona mapping. A key with roles `["analyst", "data_engineer"]` could map to either persona if both roles are configured.

## Multiple Keys

You can define multiple keys with different access levels:

```yaml
auth:
  api_keys:
    enabled: true
    keys:
      # Full administrative access
      - key: ${API_KEY_ADMIN}
        name: "admin"
        roles: ["admin"]

      # Read and write data access
      - key: ${API_KEY_DATA_TEAM}
        name: "data-team"
        roles: ["analyst"]

      # Read-only access
      - key: ${API_KEY_VIEWER}
        name: "dashboard"
        roles: ["viewer"]

      # Service account for ETL
      - key: ${API_KEY_ETL}
        name: "etl-service"
        roles: ["service", "write"]
```

## Combined with OIDC

API keys work alongside OIDC authentication:

```yaml
auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com"
    # ... OIDC config

  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_SERVICE}
        name: "background-service"
        roles: ["service"]
```

The platform checks authentication in order:
1. If a Bearer token looks like a JWT, validate via OIDC
2. Otherwise, check against API keys
3. If neither matches, reject the request

## Client Configuration

### Claude Code

```bash
# Set environment variable
export MCP_API_KEY="your-api-key"

# Add server with API key
claude mcp add mcp-data-platform -- \
  mcp-data-platform --config platform.yaml
```

The platform reads the API key from the request headers set by the MCP client.

### Claude Desktop

```json
{
  "mcpServers": {
    "mcp-data-platform": {
      "command": "mcp-data-platform",
      "args": ["--config", "platform.yaml"],
      "env": {
        "MCP_API_KEY": "your-api-key"
      }
    }
  }
}
```

### HTTP Clients

```bash
# SSE transport with API key header
curl -H "Authorization: Bearer your-api-key" \
  http://localhost:8080/sse

# Or as query parameter
curl "http://localhost:8080/sse?api_key=your-api-key"
```

## Security Best Practices

**Never commit API keys to version control:**
```yaml
# Bad - key in config file
keys:
  - key: "abc123-actual-key"
    name: "service"

# Good - key from environment
keys:
  - key: ${API_KEY_SERVICE}
    name: "service"
```

**Use different keys for different purposes:**
- Separate keys for production vs development
- Separate keys for different services
- Separate keys for different access levels

**Rotate keys periodically:**
1. Add a new key with the same roles
2. Update clients to use the new key
3. Remove the old key from configuration

**Monitor key usage:**
Enable audit logging to track API key usage:
```yaml
audit:
  enabled: true
  log_tool_calls: true
```

## Key Validation

The platform validates API keys by:
1. Checking the key exists in configuration
2. Matching exactly (case-sensitive)
3. Key must be non-empty

Invalid keys return 401 Unauthorized.

## Troubleshooting

**Key rejected:**
- Verify the key matches exactly (no extra whitespace)
- Check environment variable is set correctly
- Ensure `api_keys.enabled: true`

**Wrong roles applied:**
- Check the key definition in configuration
- Verify the correct key is being used
- Review persona mapping for those roles

**Key works locally but not in production:**
- Environment variables may differ between environments
- Check configuration is using the right variable names

## Next Steps

- [OAuth 2.1 Server](oauth-server.md) - Dynamic client authentication
- [Personas](../personas/overview.md) - Role-based access control
