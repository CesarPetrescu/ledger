# Security Policy

## Reporting a vulnerability

Do not open a public issue for suspected vulnerabilities, exposed credentials, authentication bypasses, SSRF, token-handling flaws, or deployment-specific data leaks.

Use GitHub's private vulnerability reporting for this repository:

<https://github.com/CesarPetrescu/ledger/security/advisories/new>

Include:

- the affected component and version or commit;
- reproduction steps or a minimal proof of concept;
- expected and observed behavior;
- potential impact;
- any suggested mitigation.

Do not include real tokens, passwords, private keys, project content, database exports, IP addresses, hostnames, or other operator-specific identifiers. Use fictional values and reserved example domains.

## Supported versions

Until tagged releases are published, security fixes target the current `main` branch. After releases begin, only the latest release line and `main` are supported unless a release note says otherwise.

## Response targets

Maintainers aim to acknowledge a complete private report within five business days, provide an initial assessment within ten business days, and coordinate disclosure after a fix is available. Complex reports may require more time; these are targets, not guarantees.

## Security model

Ledger is intended to run behind an HTTPS reverse proxy. Operators are responsible for:

- keeping `.env`, backups, OAuth credentials, and inference credentials outside version control;
- restricting the trusted-proxy CIDR to the actual upstream proxy;
- preventing direct exposure of PostgreSQL and `ledger-index`;
- protecting the approval password and rotating it after suspected disclosure;
- revoking OAuth clients after device or credential loss;
- applying supported Go, container-base, PostgreSQL, and dependency updates;
- encrypting and access-controlling backups.

## Public-repository privacy

Public examples and tests must not contain real project names, personal notes, deployment domains, LAN addresses, account identifiers, or host-state information. Store local operational context under `.private/`; Git and Docker ignore that directory.
