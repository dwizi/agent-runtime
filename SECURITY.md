# Security Policy

## Supported Versions

The project is pre-1.0. Security fixes are applied to the latest release line.

| Version | Supported |
| --- | --- |
| 0.1.x | yes |
| < 0.1.0 | no |

## Reporting a Vulnerability

Please do not report security vulnerabilities in public GitHub issues.

Use GitHub's private vulnerability reporting for this repository:

1. Open the repository Security tab.
2. Create a private vulnerability report.
3. Include reproduction details, impact, and affected configuration.

If private reporting is unavailable, contact maintainers through a private
channel and include the same details.

## What to Include

- affected version/commit
- threat model or attack path
- step-by-step reproduction
- impact assessment
- any proposed mitigation

## Response Process

Maintainers aim to:

- acknowledge reports within 3 business days
- provide an initial triage status within 7 business days
- ship a fix or mitigation as quickly as practical based on severity

## Security Best Practices for Deployers

- keep `.env` and connector tokens out of source control
- rotate Telegram/Discord/API credentials regularly
- enforce mTLS on admin endpoints
- keep sandbox command allowlist minimal
- review pending action approvals in admin channels frequently
