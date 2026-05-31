# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |
| < latest | No       |

## Reporting a Vulnerability

Please report security issues privately via [GitHub Security Advisories](https://github.com/zoharbabin/web-researcher-mcp/security/advisories/new).

Do not open public issues for security vulnerabilities.

Expected response time: 48 hours for acknowledgment, 7 days for a fix plan.

## PSIRT Process

Reports follow a five-stage Product Security Incident Response Team (PSIRT) process:

1. **Receive** — A private report arrives via GitHub Security Advisories. Acknowledgment is sent within 48 hours.
2. **Triage** — The report is validated and scored with [CVSS v4.0](https://www.first.org/cvss/v4-0/). A fix plan and target timeline are communicated within 7 days.
3. **Remediate** — A fix is developed and tested on a private branch, with regression tests added to prevent recurrence.
4. **Disclose** — A patched release is published and a security advisory is issued. Each advisory carries a CVSS v4.0 base score and the relevant [CWE](https://cwe.mitre.org/) identifier(s). Reporters are credited unless they request otherwise.
5. **Learn** — The root cause is reviewed and, where applicable, controls or tests are hardened to close the class of issue.

Published advisories include a CVSS v4.0 vector/score and one or more CWE identifiers so downstream consumers can assess impact and map the issue to their own controls.

For the full technical security architecture, threat model, and compliance crosswalks, see [docs/SECURITY.md](docs/SECURITY.md) and [docs/SECURITY_AND_COMPLIANCE.md](docs/SECURITY_AND_COMPLIANCE.md).
