# Security Policy

## Supported versions

Ox-Agent is on the 0.x line; only the latest release receives fixes. Always run
the newest version — `ox-agent update` checks the release feed, verifies the
archive checksum, and re-syncs project assets.

## Reporting a vulnerability

Report vulnerabilities through **GitHub Private Vulnerability Reporting**:
open the repository's
[Security tab](https://github.com/oxidean/Ox-Agent/security/advisories/new)
and file a private advisory. Do not open public issues for security reports.

Please include:

- the affected version (`ox-agent version`)
- platform and install method
- reproduction steps or a proof of concept
- impact assessment, if known

Reports are triaged as soon as possible. You can expect an acknowledgement
within a few days and a coordinated fix + advisory for accepted reports.

## Scope notes

This project's threat surface is unusual — it is a CLI that downloads and
executes release binaries, and it materializes files into target repositories.
Reports are especially welcome for:

- **Release supply chain** — installer download/verification flaws
  (`install.sh`, `install.ps1`, `ox-agent update`)
- **Path confinement** — asset writes or lock entries escaping `.ox-agent/`
- **Remote skill installs** — `skills install` clones git remotes into the
  project; provenance and confinement bugs matter here
- **Harness dispatch** — `spawn-cli`/`webhook` adapters execute or contact
  external systems with packet contents

## Verifying releases yourself

Every release publishes `checksums.txt` plus a cosign keyless signature bundle
(`checksums.txt.sigstore.json`). To verify an artifact:

```bash
# checksum
sha256sum -c checksums.txt --ignore-missing

# signature (requires cosign)
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/oxidean/Ox-Agent/.github/workflows/release.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

The installers verify the checksum automatically before installing.
