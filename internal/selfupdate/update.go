// Package selfupdate replaces the running ox-agent binary with a published
// release asset after checksum verification.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/buildinfo"
)

// repo is the GitHub repository releases are fetched from.
const repo = "oxidean/Ox-Agent"

// httpClient times out stalled release downloads.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// Check reports the latest published release tag without changing anything.
func Check() (string, error) {
	req, err := http.NewRequest(http.MethodHead, "https://github.com/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolving latest release: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return "", fmt.Errorf("resolving latest release: HTTP %d", resp.StatusCode)
	}
	url := resp.Request.URL.String()
	parts := strings.Split(url, "/tag/")
	if len(parts) != 2 {
		return "", fmt.Errorf("could not determine latest release tag from %s", url)
	}
	return parts[1], nil
}

// Update downloads the release asset for the current platform, verifies its
// checksum, and atomically replaces the running binary. It reports false
// when the binary already runs the target version.
func Update(targetVersion string) (bool, string, error) {
	version := targetVersion
	if version == "" || version == "latest" {
		var err error
		version, err = Check()
		if err != nil {
			return false, "", err
		}
	}
	current := buildinfo.Version
	if current != "dev" && "v"+current == version {
		return false, version, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, "", fmt.Errorf("resolving executable path: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return false, "", err
	}
	asset := archiveName(version)
	base := "https://github.com/" + repo + "/releases/download/" + version + "/"
	checksums, err := fetchChecksums(base + "checksums.txt")
	if err != nil {
		return false, "", err
	}
	want, ok := checksums[asset]
	if !ok {
		return false, "", fmt.Errorf("no checksum published for %s", asset)
	}
	payload, err := download(base + asset)
	if err != nil {
		return false, "", err
	}
	sum := sha256.Sum256(payload)
	if got := hex.EncodeToString(sum[:]); got != want {
		return false, "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}
	binary, err := extractBinary(asset, payload)
	if err != nil {
		return false, "", err
	}
	if err := installAtomic(exe, binary); err != nil {
		return false, "", err
	}
	return true, version, nil
}

// archiveName returns the release asset file name for this platform.
func archiveName(version string) string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("ox-agent_%s_%s_%s.%s",
		strings.TrimPrefix(version, "v"), runtime.GOOS, runtime.GOARCH, ext)
}

// fetchChecksums downloads and parses checksums.txt into name→digest.
func fetchChecksums(url string) (map[string]string, error) {
	payload, err := download(url)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(payload), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			out[fields[1]] = fields[0]
		}
	}
	return out, nil
}

// download fetches a URL with a hard size cap.
func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 128<<20))
}

// extractBinary pulls the ox-agent binary out of the archive payload.
func extractBinary(asset string, payload []byte) ([]byte, error) {
	if strings.HasSuffix(asset, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			return nil, fmt.Errorf("reading zip: %w", err)
		}
		for _, f := range reader.File {
			if filepath.Base(f.Name) == "ox-agent.exe" || filepath.Base(f.Name) == "ox-agent" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("no ox-agent binary in %s", asset)
	}
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no ox-agent binary in %s", asset)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(header.Name) == "ox-agent" {
			return io.ReadAll(tr)
		}
	}
}

// installAtomic writes the new binary beside the target and renames it over,
// so a failed update never leaves a partial binary.
func installAtomic(target string, binary []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".ox-agent-update-*")
	if err != nil {
		return fmt.Errorf("staging update: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("installing update: %w", err)
	}
	return nil
}

// Uninstall removes the running binary. Project .ox-agent directories are
// repository state and are never touched.
func Uninstall() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving executable path: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("uninstall is not supported on Windows; delete %s manually", exe)
	}
	if err := os.Remove(exe); err != nil {
		return "", fmt.Errorf("removing %s: %w", exe, err)
	}
	return exe, nil
}
