# Security Policy

This repository is a publication snapshot at
<https://github.com/guopengnaivoc/NEW-API-Ultra>. For a vulnerability that is
specific to this snapshot's Docker, CI, deployment, or packaging files, use
this repository's **Security** tab and keep the report private. Vulnerabilities
in the inherited New API application should also be checked against the
upstream policy and advisory process below; the upstream New API/QuantumNous
attribution is intentionally retained.

> [!IMPORTANT]
> **Bulk reporting:** For multiple findings specific to this publication, first
> open one private draft advisory in this repository and use it to coordinate
> the remaining reports; do not send publication-only details to the upstream
> mailbox. For inherited New API findings, follow the original
> [QuantumNous/new-api security policy](https://github.com/QuantumNous/new-api/security/policy),
> including its upstream bulk-report coordination instructions.

## Supported Versions

This is an initial publication snapshot, not a maintained release series. There
is currently no guaranteed security-update schedule or response SLA. Any future
support policy will be announced in this file and in published release notes.
Until then, treat every deployment as operator-maintained and compare inherited
application issues with the upstream New API advisories.

## Reporting a Vulnerability

We take security vulnerability reports very seriously. If you discover a security issue, please follow the steps below for responsible disclosure.

### How to Report

**Do NOT** report security vulnerabilities in public GitHub Issues.

To report an issue in this publication, use the GitHub Security Advisories tab
to [open a draft security advisory](https://github.com/guopengnaivoc/NEW-API-Ultra/security/advisories/new).
This is the preferred method because it provides a built-in private channel.
For an inherited upstream issue, also follow the original
[QuantumNous/new-api advisory process](https://github.com/QuantumNous/new-api/security/advisories/new).
The original upstream advisory URL is retained here for attribution and
cross-checking; it is not a substitute for opening a private report in this
publication repository when the defect is introduced by its packaging files.

For this publication there is no separate maintainer mailbox yet. Do not send
publication-only vulnerability details to the upstream mailbox below. It is
retained only for inherited New API/QuantumNous issues:

- **Upstream email (inherited issues only):** [support@quantumnous.com](mailto:support@quantumnous.com)
- **Subject:** `[SECURITY] Security Vulnerability Report`

### What to Include

To help us understand and resolve the issue more quickly, please include the following information in your report:

1. **Vulnerability Type** - Brief description of the vulnerability (e.g., SQL injection, XSS, authentication bypass, etc.)
2. **Affected Component** - Affected file paths, endpoints, or functional modules
3. **Reproduction Steps** - Detailed steps to reproduce
4. **Impact Assessment** - Potential security impact and severity assessment
5. **Proof of Concept** - If possible, provide proof of concept code or screenshots (do not test in production environments)
6. **Suggested Fix** - If you have a fix suggestion, please provide it
7. **Your Contact Information** - So we can communicate with you

## Response Process

Publication-specific reports are reviewed on a best-effort basis through the
private advisory. No acknowledgment, assessment, or remediation deadline is
promised. When a publication-specific fix is prepared, the advisory can be used
to coordinate disclosure and credit. For inherited application behavior, follow
the response process stated by the upstream New API project.

## Security Best Practices

When deploying and using New API, we recommend following these security best practices:

### Deployment Security

- **Use HTTPS:** Always serve over HTTPS to ensure transport layer security
- **Firewall Configuration:** Only open necessary ports and restrict access to management interfaces
- **Update Review:** Track this repository and upstream New API advisories; assess applicable updates before deploying them
- **Environment Isolation:** Use separate database and Redis instances in production

### API Key Security

- **Key Protection:** Do not expose API keys in client-side code or public repositories
- **Least Privilege:** Create different API keys for different purposes, following the principle of least privilege
- **Regular Rotation:** Rotate API keys regularly
- **Monitor Usage:** Monitor API key usage and detect anomalies promptly

### Database Security

- **Strong Passwords:** Use strong passwords to protect database access
- **Network Isolation:** Database should not be directly exposed to the public internet
- **Regular Backups:** Regularly backup the database and verify backup integrity
- **Access Control:** Limit database user permissions, following the principle of least privilege

## Security-Related Configuration

Please ensure the following security-related environment variables and settings are properly configured:

- `SESSION_SECRET` - Use a strong random string
- `SQL_DSN` - Ensure database connection uses secure configuration
- `REDIS_CONN_STRING` - If using Redis, ensure secure connection
- `DATA_ENCRYPTION_KEYS` and `DATA_ENCRYPTION_ACTIVE_KEY_ID` - Keep the keyring
  stable and do not disclose it; it protects stored channel credentials

For detailed configuration instructions, please refer to the project documentation.

## Disclaimer

This project is provided "as is" without any express or implied warranty. Users should assess the security risks of using this software in their environment.
