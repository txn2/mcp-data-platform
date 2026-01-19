# Security Policy

## Supported Versions

We release patches for security vulnerabilities in the following versions:

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability within {{project-name}}, please report it responsibly.

### How to Report

**Please do NOT report security vulnerabilities through public GitHub issues.**

Instead, please report them via one of the following methods:

1. **GitHub Security Advisories** (Preferred): Use [GitHub's private vulnerability reporting](https://github.com/{{github-org}}/{{project-name}}/security/advisories/new) to report the vulnerability directly.

2. **Email**: Send an email to {{maintainer-email}} with:
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

When deploying {{project-name}}:

1. **Credentials Management**
   - Never commit credentials to version control
   - Use environment variables or secret managers for sensitive configuration
   - Rotate credentials regularly

2. **Network Security**
   - Use TLS for all connections where available
   - Deploy behind a firewall or VPN when possible

3. **Access Control**
   - Grant minimal necessary permissions
   - Consider using read-only credentials where applicable

## Security Features

{{project-name}} includes several security features by default:

- **Read-Only Mode**: Configurable to prevent write operations
- **TLS Support**: Full TLS/SSL support where applicable
- **Input Validation**: All inputs are validated before processing

## Security Updates

Security updates are released as patch versions and announced via:

- GitHub Security Advisories
- Release notes
- The project README

We recommend always running the latest version of {{project-name}}.
