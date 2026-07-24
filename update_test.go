package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func makeUpdateArchive(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestUpdateAssetNameSupportsReleaseTargets(t *testing.T) {
	for _, test := range []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "acc-darwin-arm64"},
		{"darwin", "amd64", "acc-darwin-amd64"},
		{"linux", "arm64", "acc-linux-arm64"},
		{"linux", "amd64", "acc-linux-amd64"},
	} {
		got, err := updateAssetName(test.goos, test.goarch)
		if err != nil || got != test.want {
			t.Fatalf("updateAssetName(%q, %q) = %q, %v; want %q", test.goos, test.goarch, got, err, test.want)
		}
	}
	if _, err := updateAssetName("windows", "amd64"); err == nil {
		t.Fatal("unsupported update target was accepted")
	}
}

func TestUpdateChecksumAndArchiveValidation(t *testing.T) {
	archive := makeUpdateArchive(t, "acc-linux-amd64", []byte("binary"))
	sum := sha256.Sum256(archive)
	parsed, err := parseUpdateChecksum([]byte(hex.EncodeToString(sum[:]) + "  acc-linux-amd64.tar.gz\n"))
	if err != nil || parsed != sum {
		t.Fatalf("checksum parse = %x, %v; want %x", parsed, err, sum)
	}
	binary, err := extractUpdateBinary(archive, "acc-linux-amd64")
	if err != nil || string(binary) != "binary" {
		t.Fatalf("extract = %q, %v", binary, err)
	}
	if _, err := extractUpdateBinary(archive, "acc-linux-arm64"); err == nil {
		t.Fatal("archive missing the expected asset was accepted")
	}
}

func TestInstallUpdatedBinaryIsExecutableAndAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release updater supports macOS and Linux")
	}
	destination := filepath.Join(t.TempDir(), "acc")
	old := []byte("#!/bin/sh\necho old acc\n")
	if err := os.WriteFile(destination, old, 0755); err != nil {
		t.Fatal(err)
	}
	updated := []byte("#!/bin/sh\necho acc updated\n")
	if err := installUpdatedBinary(destination, updated); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, updated) {
		t.Fatalf("installed body = %q, want %q", body, updated)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Fatalf("installed mode = %o, want executable", info.Mode().Perm())
	}
}
