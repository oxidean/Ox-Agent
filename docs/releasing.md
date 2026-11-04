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

## Release workflow

Create an annotated `v*.*.*` tag on the commit to release and push it:

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

The tag triggers `.github/workflows/release.yml`. The workflow checks out the
tag, runs tests and vet, builds cross-platform binaries, creates checksums, and
publishes the GitHub Release. The workflow can also be dispatched manually to
republish an existing tag.

The previous npm/GitHub Packages release process is retired.
