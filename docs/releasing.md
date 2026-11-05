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
  --certificate-identity-regexp 'https://github.com/oxidean/Ox-Agent/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

## Release workflow

Create an annotated `v*.*.*` tag on the commit to release and push it:

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
