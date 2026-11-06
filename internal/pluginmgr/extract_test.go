package pluginmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setMaxLayerBytes(t *testing.T, n int64) {
	t.Helper()
	old := maxLayerBytes
	maxLayerBytes = n
	t.Cleanup(func() { maxLayerBytes = old })
}

type tarEntry struct {
	name     string
	typeflag byte
	body     []byte
}

func tarGzEntries(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o755, Size: int64(len(e.body))}
		if e.typeflag != tar.TypeReg {
			hdr.Size = 0
			hdr.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write(e.body); err != nil {
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

func TestExtractBinary(t *testing.T) {
	bin := []byte("#!/bin/sh\n")
	many := make([]tarEntry, maxTarEntries+1)
	for i := range many {
		many[i] = tarEntry{name: fmt.Sprintf("f%d", i), typeflag: tar.TypeReg}
	}
	cases := []struct {
		name    string
		entries []tarEntry
		limit   int64
		err     string
	}{
		{name: "ok", entries: []tarEntry{{"LICENSE", tar.TypeReg, []byte("MIT")}, {"dist/lfed-x", tar.TypeReg, bin}}},
		{name: "symlink skipped", entries: []tarEntry{{"lfed-x", tar.TypeSymlink, nil}, {"lfed-x", tar.TypeReg, bin}}},
		{name: "only symlink", entries: []tarEntry{{"lfed-x", tar.TypeSymlink, nil}}, err: "no file named lfed-x"},
		{name: "too many entries", entries: append(many, tarEntry{"lfed-x", tar.TypeReg, bin}), err: "more than 64 entries"},
		{name: "expansion", entries: []tarEntry{{"lfed-x", tar.TypeReg, make([]byte, 1<<20)}}, limit: 64 << 10, err: "expands to more than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.limit != 0 {
				setMaxLayerBytes(t, c.limit)
			}
			dst := filepath.Join(t.TempDir(), "out")
			err := extractBinary(bytes.NewReader(tarGzEntries(t, c.entries)), "lfed-x", dst)
			if c.err == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(dst); !bytes.Equal(got, bin) {
					t.Errorf("extracted %q", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("err = %v, want containing %q", err, c.err)
			}
		})
	}
}
