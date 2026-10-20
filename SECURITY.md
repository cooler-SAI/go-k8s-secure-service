# Security Policy

## Supported Versions

Only the latest release receives security fixes.

| Version | Supported |
| :--- | :--- |
| Latest `v1.x` release | Yes |
| Older releases | No |

## Reporting a Vulnerability

Please **do not** open a public issue for security problems.

Report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/cooler-SAI/go-k8s-secure-service/security/advisories/new)
("Security" tab, then "Report a vulnerability").

Please include:

- a description of the issue and its impact,
- steps or a proof of concept to reproduce it,
- the affected version or commit.

You can expect an acknowledgement within 7 days. Confirmed issues are fixed in
a new release and disclosed in a GitHub Security Advisory once a fix is
available.

## Verifying Releases

Release images are signed with Cosign (keyless) and ship with SBOM
attestations. See the "Supply Chain" section of the [README](README.md) for
the verification command.
