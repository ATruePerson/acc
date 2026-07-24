package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	accReleaseBaseURL  = "https://github.com/ATruePerson/acc/releases/latest/download"
	maxUpdateArchive   = 128 << 20
	maxUpdateBinary    = 256 << 20
	maxUpdateChecksum  = 8 << 10
	updateHTTPTimeout  = 2 * time.Minute
	updateProbeTimeout = 10 * time.Second
)

// update is intercepted before main's normal command dispatcher so older ACC
// builds can gain the updater without restructuring the existing CLI surface.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		os.Exit(runUpdateCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
}

func runUpdateCommand(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errOut, "  Usage: acc update")
		return 2
	}
	asset, err := updateAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		fmt.Fprintf(errOut, "  Cannot update ACC on this platform: %v\n", err)
		return 1
	}
	destination, err := updateDestination()
	if err != nil {
		fmt.Fprintf(errOut, "  Cannot choose an install path: %v\n", err)
		return 1
	}
	client := &http.Client{Timeout: updateHTTPTimeout}
	archiveURL := accReleaseBaseURL + "/" + asset + ".tar.gz"
	checksumURL := archiveURL + ".sha256"
	fmt.Fprintf(out, "  Downloading the latest %s release...\n", asset)
	archive, err := downloadUpdateFile(client, archiveURL, maxUpdateArchive)
	if err != nil {
		fmt.Fprintf(errOut, "  Update download failed: %v\n", err)
		return 1
	}
	checksumFile, err := downloadUpdateFile(client, checksumURL, maxUpdateChecksum)
	if err != nil {
		fmt.Fprintf(errOut, "  Checksum download failed: %v\n", err)
		return 1
	}
	expectedChecksum, err := parseUpdateChecksum(checksumFile)
	if err != nil {
		fmt.Fprintf(errOut, "  Invalid release checksum: %v\n", err)
		return 1
	}
	actualChecksum := sha256.Sum256(archive)
	if actualChecksum != expectedChecksum {
		fmt.Fprintln(errOut, "  Refusing update: release checksum does not match.")
		return 1
	}
	binary, err := extractUpdateBinary(archive, asset)
	if err != nil {
		fmt.Fprintf(errOut, "  Invalid release archive: %v\n", err)
		return 1
	}
	if err := installUpdatedBinary(destination, binary); err != nil {
		fmt.Fprintf(errOut, "  Could not install the update: %v\n", err)
		fmt.Fprintln(errOut, "  Set ACC_BINDIR to a writable directory and try again.")
		return 1
	}
	fmt.Fprintf(out, "  Updated ACC to the latest release at %s\n", destination)
	fmt.Fprintln(out, "  Your config, provider keys, Codex baseline, and authentication files were not changed.")
	return 0
}

func updateAssetName(goos, goarch string) (string, error) {
	if goos != "darwin" && goos != "linux" {
		return "", fmt.Errorf("unsupported operating system %q", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture %q", goarch)
	}
	return fmt.Sprintf("acc-%s-%s", goos, goarch), nil
}

func updateDestination() (string, error) {
	if bindir := strings.TrimSpace(os.Getenv("ACC_BINDIR")); bindir != "" {
		absolute, err := filepath.Abs(bindir)
		if err != nil {
			return "", err
		}
		return filepath.Join(absolute, "acc"), nil
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		if filepath.Base(executable) == "acc" && !strings.Contains(executable, "go-build") {
			return executable, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "acc"), nil
}

func downloadUpdateFile(client *http.Client, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "acc-updater")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("%s returned HTTP %d", url, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s exceeded the %d-byte limit", url, limit)
	}
	return body, nil
}

func parseUpdateChecksum(body []byte) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return result, fmt.Errorf("checksum file is empty")
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil || len(decoded) != sha256.Size {
		return result, fmt.Errorf("checksum is not a SHA-256 value")
	}
	copy(result[:], decoded)
	return result, nil
}

func extractUpdateBinary(archive []byte, expectedName string) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Name != expectedName {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("%s is not a regular file", expectedName)
		}
		if header.Size < 1 || header.Size > maxUpdateBinary {
			return nil, fmt.Errorf("%s has invalid size %d", expectedName, header.Size)
		}
		binary, err := io.ReadAll(io.LimitReader(tarReader, maxUpdateBinary+1))
		if err != nil {
			return nil, err
		}
		if int64(len(binary)) != header.Size {
			return nil, fmt.Errorf("%s was truncated", expectedName)
		}
		return binary, nil
	}
	return nil, fmt.Errorf("archive does not contain %s", expectedName)
}

func installUpdatedBinary(destination string, binary []byte) error {
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".acc-update-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0755); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(binary); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := probeUpdatedBinary(temporaryPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}

func probeUpdatedBinary(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), updateProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "help")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("updated binary validation timed out")
	}
	if err != nil {
		return fmt.Errorf("updated binary validation failed: %w", err)
	}
	if !strings.Contains(strings.ToLower(string(output)), "acc") {
		return fmt.Errorf("updated binary returned unexpected help output")
	}
	return nil
}
