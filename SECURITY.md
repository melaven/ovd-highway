# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.0.x   | :white_check_mark: |

Only the latest minor release receives security fixes.

## Reporting a Vulnerability

Please **do not** open a public issue for security vulnerabilities.

Report privately via GitHub's [private vulnerability reporting](https://github.com/melaven/ovd-highway/security/advisories/new)
or by emailing the maintainer directly.

Include:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof of concept
- Affected version / commit
- Any suggested mitigation

You can expect an initial acknowledgement within **72 hours** and a
status update within **7 days**.

## Scope

In scope:

- Authentication/authorization bypass on `/api/v1/*`, `/ws`, or probes
- Secret leakage (tokens, `.env` contents, session material)
- Injection flaws in SQL (pgx), Redis keys, or LLM prompts
- Dependency vulnerabilities with a practical exploit path

Out of scope:

- Denial of service against third-party APIs (Telegram, Reddit, LLM providers)
- Findings in dependencies without a demonstrated exploit against this codebase
- Social engineering or attacks requiring physical access

## Secrets Handling

- Never commit `.env`, `session.json`, or real credentials — they are git-ignored.
- `ADMIN_API_TOKEN` protects `/api/v1/*` and `/ws`; rotate it if exposed.
- Telegram MTProto session files are local-only; treat them as credentials.
