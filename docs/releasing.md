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

A `v*.*.*` tag triggers `.github/workflows/release.yml`. The workflow checks out
the tag, runs tests and vet, builds cross-platform binaries, creates checksums,
and publishes the GitHub Release.

The previous npm/GitHub Packages release process is retired.
