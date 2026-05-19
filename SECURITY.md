# Security Policy

## Supported Versions

XTrinode is pre-1.0. Until stable releases are published, security fixes target the default branch
and the latest published release tag when practical.

| Version | Supported |
| --- | --- |
| `main` | Yes |
| Latest release | Best effort |
| Older releases | No |

## Reporting a Vulnerability

Do not publish secrets, credentials, exploit details, raw auth headers, kubeconfigs, Terraform state,
or sensitive cluster configuration in public issues, discussions, pull requests, or logs.

Preferred reporting path:

1. Use GitHub private vulnerability reporting from the repository Security tab when available.
2. If private reporting is not available, open a minimal public issue that says a security contact
   path is needed. Do not include exploit details or sensitive artifacts in that issue.

Include the following when it is safe to share privately:

- Affected component: Operator, API Server, Gateway, Helm, Terraform, CI, or documentation.
- Affected version, image tag, chart version, or commit SHA.
- Impact and whether the issue is remotely reachable.
- Minimal reproduction steps with secrets and cluster identifiers removed.
- Suggested fix or mitigation, if known.

## Response Expectations

Maintainers will triage reports as project capacity allows. For confirmed vulnerabilities, fixes
should prefer the smallest patch that removes the risk without exposing report details before users
have a reasonable update path.

## Security Scope

Security-sensitive areas include authentication, token handling, CORS, gateway routing, Kubernetes
RBAC, generated manifests, image publishing, release tagging, Terraform state, Secret handling, and
any path that could expose Trino queries, catalogs, or credentials.
