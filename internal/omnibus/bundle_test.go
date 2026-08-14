package omnibus

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// makeTarball builds a gzipped tar in the shape GitHub serves: every path under
// one wrapper directory.
func makeTarball(t *testing.T, wrapper string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		hdr := &tar.Header{
			Name:     wrapper + "/" + name,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write body %s: %v", name, err)
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

func TestReadTarballClassifies(t *testing.T) {
	data := makeTarball(t, "hihipy-demo-abc123", map[string][]byte{
		"README.md":       []byte("# demo\n"),
		"src/main.go":     []byte("package main\n"),
		"q.sql":           []byte("select 1;\n"),
		"assets/logo.png": append([]byte("\x89PNG"), make([]byte, 40)...),
		"blob.dat":        {'a', 'b', 0, 'c'},
		"big.txt":         bytes.Repeat([]byte("x"), 2048),
		"bad.txt":         {0xff, 0xfe, 'h', 'i', 0xff},
	})

	b, err := ReadTarball(data, ReadOptions{MaxBytes: 1024})
	if err != nil {
		t.Fatalf("ReadTarball: %v", err)
	}

	var got []string
	for _, f := range b.Files {
		got = append(got, f.Path)
	}
	want := []string{"README.md", "q.sql", "src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("included = %v, want %v", got, want)
	}

	reasons := map[string]string{}
	for _, s := range b.Skipped {
		reasons[s.Path] = s.Reason
	}
	for path, want := range map[string]string{
		"assets/logo.png": "a binary file",
		"blob.dat":        "binary content",
		"bad.txt":         "not valid UTF-8",
	} {
		if reasons[path] != want {
			t.Errorf("skip reason for %s = %q, want %q", path, reasons[path], want)
		}
	}
	if reasons["big.txt"] != "over the size limit" {
		t.Errorf("big.txt reason = %q, want the size category", reasons["big.txt"])
	}
}

func TestReadmeSortsFirst(t *testing.T) {
	data := makeTarball(t, "w", map[string][]byte{
		"zzz.py":       []byte("x\n"),
		"a/b/deep.py":  []byte("x\n"),
		"README.md":    []byte("x\n"),
		"aaa.py":       []byte("x\n"),
		"a/shallow.py": []byte("x\n"),
	})
	b, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md", "aaa.py", "zzz.py", "a/shallow.py", "a/b/deep.py"}
	for i, f := range b.Files {
		if f.Path != want[i] {
			t.Fatalf("order = %v, want %v", b.Files, want)
		}
	}
}

func TestFenceLongerThanContent(t *testing.T) {
	cases := map[string]string{
		"plain":                "```",
		"a ``` fence":          "````",
		"a ````` deeper fence": "``````",
	}
	for text, want := range cases {
		if got := fenceFor(text); got != want {
			t.Errorf("fenceFor(%q) = %q, want %q", text, got, want)
		}
	}
}
