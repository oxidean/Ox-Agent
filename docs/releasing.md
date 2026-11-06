# Release Ox-Agent

Releases publish standalone Go binaries through GitHub Releases.

## Local checks

```bash
go test ./...
go vet ./...
go build ./cmd/ox-agent
```

## Release artifacts

GoReleaser creates archives for supported platforms and architectures, plus
`checksums.txt`. The shell installer verifies the checksum before installation.
`checksums.txt` is signed keylessly with cosign during the release workflow;
the Sigstore bundle publishes alongside it as `checksums.txt.sigstore.json`:

```bash
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github\.com/oxidean/Ox-Agent/\.github/workflows/release\.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

The identity pins the signing to the release workflow on tag refs. Releases
republished through `workflow_dispatch` carry a branch ref instead — verify
those against `@refs/heads/<dispatch-branch>`:

```bash
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github\.com/oxidean/Ox-Agent/\.github/workflows/release\.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Republishing a tag only produces a signature bundle when the tag's tree carries
the signing configuration — `v0.0.1` predates it, so republishing that tag
rebuilds unsigned artifacts.

## Release workflow

Merges to `main` automatically tag the next patch release:
`.github/workflows/tag-release.yml` computes the next `v*.*.*` patch tag from
the latest existing tag, pushes it, and dispatches `release.yml` against it.
(Tags pushed by `GITHUB_TOKEN` do not fire `on: push` workflows, which is why
the release is dispatched rather than triggered by the push.)

For a minor or major bump — or to release a specific commit — create an
annotated tag manually and push it:

```bash
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The tag triggers `.github/workflows/release.yml`. The workflow checks out the
tag, runs tests and vet, builds cross-platform binaries, creates checksums, and
publishes the GitHub Release. The workflow can also be dispatched manually to
republish an existing tag. GoReleaser does not replace existing release
assets, so delete the published release first (the tag is kept):

```bash
gh release delete v0.1.0 --yes
gh workflow run release.yml -f tag=v0.1.0
```

The previous npm/GitHub Packages release process is retired.
