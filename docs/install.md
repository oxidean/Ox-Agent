# Install Ox-Agent

Ox-Agent is a globally installed Go binary. Target repositories do not install
it through npm, Bun, or project dependencies.

## macOS and Linux

Install the latest release:

```bash
curl -fsSL \
  https://github.com/oxidean/Ox-Agent/releases/latest/download/install.sh \
  | bash
```

Install a specific release:

```bash
OX_AGENT_VERSION=v0.2.0 \
  curl -fsSL \
  https://github.com/oxidean/Ox-Agent/releases/download/v0.2.0/install.sh \
  | bash
```

The default destination is `~/.local/bin`. Set `OX_AGENT_INSTALL_DIR` to use a
different user-owned directory. Add the destination to `PATH` if necessary.

## Windows

Run the PowerShell installer from a release checkout or downloaded release:

```powershell
$env:OX_AGENT_VERSION = "v0.2.0"
irm https://github.com/oxidean/Ox-Agent/releases/download/v0.2.0/install.ps1 | iex
```

## Verify

```bash
ox-agent version
ox-agent doctor
```

## Manual installation

Download the archive matching the operating system and architecture from the
GitHub Release. Download `checksums.txt`, verify the archive's SHA-256 digest,
extract `ox-agent`, and place it in a directory on `PATH`.

## Upgrade and uninstall

The update command is planned. Until it is available, install a newer release
over the existing binary after verifying its checksum. To uninstall, remove
the binary from the directory where it was installed. Project-local
`.ox-agent/` directories are not removed by uninstalling the global binary.
