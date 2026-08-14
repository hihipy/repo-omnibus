package omnibus

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// binaryExt holds extensions never worth including as text. Anything not listed
// still faces the null-byte and UTF-8 checks, so this is a shortcut rather than
// the only defence.
var binaryExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".ico": true, ".icns": true, ".tif": true, ".tiff": true,
	".pdf": true, ".zip": true, ".gz": true, ".bz2": true, ".xz": true,
	".7z": true, ".tar": true, ".rar": true, ".dmg": true, ".pkg": true,
	".xlsx": true, ".xlsm": true, ".xls": true, ".docx": true, ".doc": true,
	".pptx": true, ".ppt": true, ".key": true, ".numbers": true, ".pages": true,
	".sqlite": true, ".db": true, ".pbix": true, ".pbip": true,
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	".mp3": true, ".mp4": true, ".mov": true, ".wav": true, ".avi": true,
	".webm": true, ".psd": true, ".ai": true, ".sketch": true,
	".pyc": true, ".so": true, ".dylib": true, ".dll": true, ".exe": true,
	".class": true, ".jar": true, ".rds": true, ".rdata": true, ".parquet": true,
	".blend": true, ".fbx": true, ".obj": true, ".3ds": true, ".glb": true,
}

// File is one text file destined for the bundle. Note explains where the text
// came from when it was not read directly, as with an extracted spreadsheet.
type File struct {
	Path string
	Text string
	Note string
}

// Skipped records a file left out and why, so no exclusion is silent. Reason is
// a category that groups; Detail is what decided this one file.
type Skipped struct {
	Path   string
	Reason string
	Detail string
	Size   int64
}

// Bundle is everything collected from one repository.
type Bundle struct {
	Repo    Repo
	Files   []File
	Skipped []Skipped
}

// Chars totals the text the bundle carries, for the size and token estimates.
func (b Bundle) Chars() int {
	n := 0
	for _, f := range b.Files {
		n += len(f.Text)
	}
	return n
}

// ReadOptions bounds and shapes what ReadTarball keeps.
type ReadOptions struct {
	MaxBytes         int64
	IncludeLicenses  bool
	IncludeGenerated bool
	SkipOffice       bool // link spreadsheets and documents instead of extracting
}

// ReadTarball splits a repository tarball into includable files and skipped
// entries.
func ReadTarball(data []byte, opt ReadOptions) (Bundle, error) {
	var b Bundle

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return b, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return b, fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Every path in a GitHub tarball sits under one wrapper directory.
		rel := hdr.Name
		if i := strings.Index(rel, "/"); i >= 0 {
			rel = rel[i+1:]
		}
		if rel == "" {
			continue
		}

		base := strings.ToLower(path.Base(rel))
		if licenseNames[base] && !opt.IncludeLicenses {
			b.Skipped = append(b.Skipped, Skipped{rel, "license text, identical in every repository", "", hdr.Size})
			continue
		}
		if lockNames[base] && !opt.IncludeGenerated {
			b.Skipped = append(b.Skipped, Skipped{rel, "a dependency lock file, written by a resolver", "", hdr.Size})
			continue
		}

		ext := strings.ToLower(path.Ext(rel))

		// Spreadsheets and documents are zips of XML, so their text can be
		// recovered. A repository whose product is a spreadsheet is otherwise
		// represented by its README alone.
		if !opt.SkipOffice && officeExt[ext] && hdr.Size <= maxOfficeBytes {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return b, fmt.Errorf("reading %s: %w", rel, err)
			}
			if text, ok := ExtractOffice(rel, raw); ok {
				b.Files = append(b.Files, File{
					Path: rel,
					Text: text,
					Note: fmt.Sprintf("text extracted from the %s file, %s",
						strings.TrimPrefix(ext, "."), humanBytes(hdr.Size)),
				})
				continue
			}
			b.Skipped = append(b.Skipped, Skipped{
				rel, "no text could be extracted", ext, hdr.Size})
			continue
		}

		if binaryExt[ext] {
			b.Skipped = append(b.Skipped, Skipped{rel, "a binary file", ext, hdr.Size})
			continue
		}
		if hdr.Size > opt.MaxBytes {
			b.Skipped = append(b.Skipped, Skipped{
				rel, "over the size limit", commas(hdr.Size) + " bytes", hdr.Size})
			continue
		}

		raw, err := io.ReadAll(tr)
		if err != nil {
			return b, fmt.Errorf("reading %s: %w", rel, err)
		}
		head := raw
		if len(head) > 8192 {
			head = head[:8192]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			b.Skipped = append(b.Skipped, Skipped{rel, "binary content", "", hdr.Size})
			continue
		}
		if !utf8.Valid(raw) {
			b.Skipped = append(b.Skipped, Skipped{rel, "not valid UTF-8", "", hdr.Size})
			continue
		}

		text := string(raw)
		// The file is text. Whether it is worth reading is a separate question,
		// and one the name cannot answer.
		if !opt.IncludeGenerated {
			if reason, detail := TriageText(rel, text); reason != "" {
				b.Skipped = append(b.Skipped, Skipped{rel, reason, detail, hdr.Size})
				continue
			}
		}
		b.Files = append(b.Files, File{Path: rel, Text: text})
	}

	sort.Slice(b.Files, func(i, j int) bool {
		return less(b.Files[i].Path, b.Files[j].Path)
	})
	sort.Slice(b.Skipped, func(i, j int) bool {
		return less(b.Skipped[i].Path, b.Skipped[j].Path)
	})
	return b, nil
}

// less orders README first, then shallower paths, then alphabetically, so a
// reader meets each repository the way its author would introduce it.
func less(a, b string) bool {
	ra, rb := isReadme(a), isReadme(b)
	if ra != rb {
		return ra
	}
	da, db := strings.Count(a, "/"), strings.Count(b, "/")
	if da != db {
		return da < db
	}
	return strings.ToLower(a) < strings.ToLower(b)
}

func isReadme(p string) bool {
	return strings.HasPrefix(strings.ToLower(path.Base(p)), "readme")
}
