# Security Policy

## Supported Versions

We release patches for security vulnerabilities in the following versions:

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability within mcp-data-platform, please report it responsibly.

### How to Report

**Please do NOT report security vulnerabilities through public GitHub issues.**

Instead, please report them via one of the following methods:

1. **GitHub Security Advisories** (Preferred): Use [GitHub's private vulnerability reporting](https://github.com/txn2/mcp-data-platform/security/advisories/new) to report the vulnerability directly.

2. **Email**: Send an email to cj@imti.co with:
   - A description of the vulnerability
   - Steps to reproduce the issue
   - Potential impact of the vulnerability
   - Any suggested fixes (optional)

### What to Expect

- **Acknowledgment**: We will acknowledge receipt of your vulnerability report within 48 hours.
- **Communication**: We will keep you informed about the progress of fixing the vulnerability.
- **Timeline**: We aim to release a fix within 90 days of the initial report, depending on complexity.
- **Credit**: We will credit you in the release notes (unless you prefer to remain anonymous).

### Security Best Practices for Users

When deploying mcp-data-platform:

1. **Credentials Management**
   - Never commit credentials to version control
   - Use environment variables or secret managers for sensitive configuration
   - Rotate credentials regularly
   - Use strong, unique API keys

2. **Network Security**
   - Use TLS for all connections where available
   - Deploy behind a firewall or VPN when possible
   - Restrict access to the MCP server to trusted clients

3. **Access Control**
   - Configure personas with minimal necessary permissions
   - Use deny rules to explicitly block dangerous operations
   - Regularly audit tool access patterns

4. **Database Security**
   - Use dedicated credentials for the audit database
   - Enable TLS for database connections
   - Regularly backup audit logs

## Security Features

mcp-data-platform includes several security features:

- **OAuth 2.1 Authentication**: Full OAuth 2.1 support with PKCE and DCR
- **OIDC Integration**: Integrate with any OIDC-compliant identity provider
- **API Key Authentication**: Support for API key-based authentication
- **Role-Based Personas**: Fine-grained tool access control with allow/deny rules
- **Audit Logging**: Comprehensive audit trail of all tool invocations
- **Input Validation**: All inputs are validated before processing
- **PKCE Support**: Proof Key for Code Exchange for secure OAuth flows

## Security Updates

Security updates are released as patch versions and announced via:

- GitHub Security Advisories
- Release notes
- The project README

We recommend always running the latest version of mcp-data-platform.
