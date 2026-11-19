package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"testing"
)

func makeArchive(t *testing.T, entries []tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := range entries {
		header := entries[i]
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if len(bodies[i]) > 0 {
			if _, err := tw.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestChecksumAndExtractBinary(t *testing.T) {
	archive := makeArchive(t, []tar.Header{{Name: "lw", Mode: 0o755, Size: 6, Typeflag: tar.TypeReg}}, [][]byte{[]byte("binary")})
	digest := sha256.Sum256(archive)
	manifest := []byte(fmt.Sprintf("%x  lw-v0.3.0-darwin-arm64.tar.gz\n", digest))
	expected, err := ExpectedChecksum(manifest, "lw-v0.3.0-darwin-arm64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(archive, expected); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractBinary(archive, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("ExtractBinary() = %q", got)
	}
}

func TestExpectedChecksumRejectsMissingDuplicateAndMalformed(t *testing.T) {
	t.Parallel()
	name := "asset.tar.gz"
	valid := fmt.Sprintf("%064x  %s\n", 1, name)
	for testName, manifest := range map[string]string{
		"missing":   fmt.Sprintf("%064x  other.tar.gz\n", 1),
		"duplicate": valid + valid,
		"malformed": "nope  " + name + "\n",
	} {
		t.Run(testName, func(t *testing.T) {
			if _, err := ExpectedChecksum([]byte(manifest), name); err == nil {
				t.Fatal("ExpectedChecksum() expected error")
			}
		})
	}
}

func TestExtractBinaryRejectsUnsafeArchives(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []tar.Header
		bodies  [][]byte
	}{
		{"traversal", []tar.Header{{Name: "../lw", Size: 1, Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}},
		{"absolute", []tar.Header{{Name: "/lw", Size: 1, Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}},
		{"symlink", []tar.Header{{Name: "lw", Typeflag: tar.TypeSymlink, Linkname: "other"}}, [][]byte{nil}},
		{"unexpected", []tar.Header{{Name: "README", Size: 1, Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}},
		{"duplicate", []tar.Header{{Name: "lw", Size: 1, Typeflag: tar.TypeReg}, {Name: "./lw", Size: 1, Typeflag: tar.TypeReg}}, [][]byte{[]byte("x"), []byte("y")}},
		{"oversized", []tar.Header{{Name: "lw", Size: 2, Typeflag: tar.TypeReg}}, [][]byte{[]byte("xx")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := makeArchive(t, tc.entries, tc.bodies)
			max := int64(1024)
			if tc.name == "oversized" {
				max = 1
			}
			if _, err := ExtractBinary(archive, max); err == nil {
				t.Fatal("ExtractBinary() expected error")
			}
		})
	}
}
