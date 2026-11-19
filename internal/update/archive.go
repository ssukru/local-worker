package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
)

func ExpectedChecksum(manifest []byte, assetName string) ([sha256.Size]byte, error) {
	var expected [sha256.Size]byte
	matches := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		matches++
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return expected, fmt.Errorf("invalid SHA-256 for %s", assetName)
		}
		copy(expected[:], digest)
	}
	if matches == 0 {
		return expected, fmt.Errorf("checksums.txt has no entry for %s", assetName)
	}
	if matches != 1 {
		return expected, fmt.Errorf("checksums.txt has duplicate entries for %s", assetName)
	}
	return expected, nil
}

func VerifySHA256(data []byte, expected [sha256.Size]byte) error {
	actual := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
		return fmt.Errorf("SHA-256 mismatch")
	}
	return nil
}

func ExtractBinary(archive []byte, maxBytes int64) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gz.Close()

	expanded := &io.LimitedReader{R: gz, N: maxBytes + (4 << 20)}
	tr := tar.NewReader(expanded)
	var binary []byte
	found := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar archive: %w", err)
		}
		clean := path.Clean(header.Name)
		if strings.HasPrefix(clean, "../") || path.IsAbs(header.Name) {
			return nil, fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if clean != "lw" {
			return nil, fmt.Errorf("unexpected archive entry %q", header.Name)
		}
		if found {
			return nil, fmt.Errorf("archive contains duplicate lw entries")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("archive entry lw is not a regular file")
		}
		if header.Size < 0 || header.Size > maxBytes {
			return nil, fmt.Errorf("archive binary exceeds %d bytes", maxBytes)
		}
		limited := io.LimitReader(tr, maxBytes+1)
		binary, err = io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("read archive binary: %w", err)
		}
		if int64(len(binary)) != header.Size {
			return nil, fmt.Errorf("archive binary is truncated")
		}
		found = true
	}
	if expanded.N <= 0 {
		return nil, fmt.Errorf("expanded archive exceeds safety limit")
	}
	if !found {
		return nil, fmt.Errorf("archive does not contain lw")
	}
	return binary, nil
}
