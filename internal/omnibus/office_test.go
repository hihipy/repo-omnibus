package omnibus

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// makeZip builds an Office file: a zip of XML parts, which is all an xlsx or
// docx is.
func makeZip(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sampleXLSX(t *testing.T) []byte {
	return makeZip(t, map[string]string{
		"xl/workbook.xml": `<workbook><sheets>
			<sheet name="Per Diem" sheetId="1"/><sheet name="Notes" sheetId="2"/>
		</sheets></workbook>`,
		"xl/sharedStrings.xml": `<sst>
			<si><t>City</t></si><si><t>Total</t></si><si><t>Tokyo</t></si>
			<si><r><t>Split </t></r><r><t>run</t></r></si>
		</sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
			<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><f>SUM(C1:C9)</f><v>421</v></c></row>
			<row r="3"><c r="A3"/><c r="B3"/></row>
			<row r="4"><c r="A4" t="s"><v>3</v></c><c r="C4"><v>7</v></c></row>
		</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="inlineStr"><is><t>Rates from State</t></is></c></row>
		</sheetData></worksheet>`,
	})
}

func TestExtractXLSX(t *testing.T) {
	text, ok := ExtractOffice("book.xlsx", sampleXLSX(t))
	if !ok {
		t.Fatal("ExtractOffice reported failure on a valid workbook")
	}
	for _, want := range []string{
		"### Per Diem",
		"### Notes",
		"| City | Total |", // shared strings resolved
		"=SUM(C1:C9)",      // the formula, not the cached value
		"Split run",        // a string split across runs
		"Rates from State", // an inline string
	} {
		if !strings.Contains(text, want) {
			t.Errorf("extract missing %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "421") {
		t.Error("the cached value should give way to the formula")
	}
	// Row 3 is empty and should not produce a table row.
	if strings.Count(text, "\n|") > 8 {
		t.Errorf("empty rows should be dropped, got:\n%s", text)
	}
}

func TestExtractXLSXPlacesCellsByReference(t *testing.T) {
	// A row that skips a column must not shift the remaining cells left.
	text, ok := ExtractOffice("book.xlsx", sampleXLSX(t))
	if !ok {
		t.Fatal("extract failed")
	}
	if !strings.Contains(text, "| Split run |  | 7 |") {
		t.Errorf("cell C4 should sit in the third column, got:\n%s", text)
	}
}

func TestExtractDOCX(t *testing.T) {
	data := makeZip(t, map[string]string{
		"word/document.xml": `<document><body>
			<p><r><t>Timeline of Events</t></r></p>
			<p><r><t>Business days are </t></r><r><t>counted from the first.</t></r></p>
			<p></p>
		</body></document>`,
	})
	text, ok := ExtractOffice("doc.docx", data)
	if !ok {
		t.Fatal("ExtractOffice reported failure on a valid document")
	}
	if !strings.Contains(text, "Timeline of Events") {
		t.Errorf("missing the heading, got:\n%s", text)
	}
	if !strings.Contains(text, "Business days are counted from the first.") {
		t.Errorf("runs should join into one paragraph, got:\n%s", text)
	}
	if strings.Contains(text, "\n\n\n") {
		t.Errorf("empty paragraphs should be dropped, got:\n%q", text)
	}
}

func TestExtractOfficeRejectsOtherTypes(t *testing.T) {
	for _, name := range []string{"a.pdf", "b.png", "c.txt", "d.pptx"} {
		if _, ok := ExtractOffice(name, []byte("not a zip")); ok {
			t.Errorf("ExtractOffice(%q) reported success", name)
		}
	}
}

func TestExtractOfficeRejectsCorruptZip(t *testing.T) {
	if _, ok := ExtractOffice("book.xlsx", []byte("PK\x03\x04 truncated")); ok {
		t.Error("a corrupt workbook should not report success")
	}
}

func TestColIndex(t *testing.T) {
	cases := map[string]int{"A1": 0, "B2": 1, "Z9": 25, "AA1": 26, "AB7": 27}
	for ref, want := range cases {
		if got := colIndex(ref); got != want {
			t.Errorf("colIndex(%q) = %d, want %d", ref, got, want)
		}
	}
}

func TestLicensesDroppedByDefault(t *testing.T) {
	data := makeTarball(t, "w", map[string][]byte{
		"README.md":   []byte("# hi\n"),
		"LICENSE.md":  []byte(strings.Repeat("Permission is hereby granted, free of charge, to any person.\n", 20)),
		"LICENSE":     []byte("more legal\n"),
		"src/main.go": []byte("package main\n"),
	})

	b, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range b.Files {
		if strings.HasPrefix(strings.ToLower(f.Path), "license") {
			t.Errorf("%s should have been dropped", f.Path)
		}
	}
	dropped := 0
	for _, s := range b.Skipped {
		if strings.Contains(s.Reason, "license text") {
			dropped++
		}
	}
	if dropped != 2 {
		t.Errorf("dropped %d license files, want 2 reported as skipped", dropped)
	}

	kept, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20, IncludeLicenses: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Files) != len(b.Files)+2 {
		t.Errorf("-include-licenses kept %d files, want %d", len(kept.Files), len(b.Files)+2)
	}
}

func TestOfficeFilesAreExtractedIntoTheBundle(t *testing.T) {
	data := makeTarball(t, "w", map[string][]byte{
		"README.md":   []byte("# hi\n"),
		"rates.xlsx":  sampleXLSX(t),
		"broken.xlsx": []byte("not a zip at all"),
	})

	b, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}

	var found *File
	for i := range b.Files {
		if b.Files[i].Path == "rates.xlsx" {
			found = &b.Files[i]
		}
	}
	if found == nil {
		t.Fatal("the workbook should have been extracted into the bundle")
	}
	if !strings.Contains(found.Text, "### Per Diem") {
		t.Errorf("extracted text looks wrong:\n%s", found.Text)
	}
	if !strings.Contains(found.Note, "extracted from the xlsx file") {
		t.Errorf("note = %q, want it to say where the text came from", found.Note)
	}

	// The unreadable one must be reported, not silently dropped.
	var reason string
	for _, s := range b.Skipped {
		if s.Path == "broken.xlsx" {
			reason = s.Reason
		}
	}
	if !strings.Contains(reason, "no text could be extracted") {
		t.Errorf("broken.xlsx reason = %q", reason)
	}

	// -skip-office restores the old behaviour.
	linked, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20, SkipOffice: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range linked.Files {
		if strings.HasSuffix(f.Path, ".xlsx") {
			t.Error("-skip-office should leave workbooks out of the files")
		}
	}
}

func TestLangLabel(t *testing.T) {
	cases := []struct {
		repo Repo
		want string
	}{
		{Repo{Language: "Python"}, "Python"},
		{Repo{Language: "Python", Langs: []string{"Python"}}, "Python"},
		{Repo{Language: "Python", Langs: []string{"Python", "Shell"}}, "Python, Shell"},
		{Repo{Language: "JavaScript", Langs: []string{"JavaScript", "CSS", "HTML"}}, "JavaScript, CSS, HTML"},
		{Repo{Langs: []string{"TeX", "Makefile"}}, "TeX, Makefile"}, // no primary field
		{Repo{}, "-"}, // GitHub files it under nothing
	}
	for _, c := range cases {
		if got := c.repo.LangLabel(); got != c.want {
			t.Errorf("LangLabel(%v) = %q, want %q", c.repo.Langs, got, c.want)
		}
	}
}

func TestSortReposByName(t *testing.T) {
	repos := []Repo{
		{Name: "tabs-2-json", Language: "JavaScript"},
		{Name: "25live-cleaner", Language: "Python"},
		{Name: "Enhanced-CAP", Language: ""},
		{Name: "ai-csv-profiler", Language: "Python"},
	}
	SortRepos(repos)

	var got []string
	for _, r := range repos {
		got = append(got, r.Name)
	}
	want := []string{"25live-cleaner", "ai-csv-profiler", "Enhanced-CAP", "tabs-2-json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v (case-insensitive A to Z)", got, want)
	}
}

func TestExtHistogramIgnoresAdminFiles(t *testing.T) {
	// A README and a CI workflow sit in every repository, so counting them
	// tells a reader nothing about which repository they are looking at.
	only := ExtHistogram([]string{"README.md", ".github/workflows/links.yml",
		"docs/guide.md", "config.toml"}, 6)
	if len(only) != 0 {
		t.Errorf("got %v, want nothing worth reporting", only)
	}
}

func TestExtHistogram(t *testing.T) {
	paths := []string{
		"scripts/postgres-xray.sql", "scripts/mysql-xray.sql", "scripts/oracle-xray.sql",
		"src/main.py", "src/util.py",
		"README.md", "LICENSE.md", // documentation: in every repo, so counted in none
		".github/workflows/links.yml", // CI config: same
		"assets/logo.png",             // binary asset
		"weird.xyzzy",                 // unknown extension
		"package-lock.json",           // counted: packaging varies between repos
	}
	got := ExtHistogram(paths, 6)

	want := []ExtCount{{".sql", 3}, {".py", 2}, {".json", 1}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (commonest first, then alphabetical)", got, want)
		}
	}
}

func TestExtHistogramCaps(t *testing.T) {
	paths := []string{"a.py", "b.sql", "c.cs", "d.go", "e.rs", "f.rb", "g.js"}
	if got := ExtHistogram(paths, 3); len(got) != 3 {
		t.Errorf("got %d entries, want the cap of 3", len(got))
	}
}

func TestLangLabelPrefersFileTypes(t *testing.T) {
	// GitHub calls every .sql file one language, which hides that sql-x-ray
	// carries eight dialect scripts.
	r := Repo{
		Language: "PLSQL",
		Langs:    []string{"PLSQL"},
		Exts:     []ExtCount{{".sql", 8}, {".py", 1}},
	}
	if got := r.LangLabel(); got != "8x.sql, 1x.py" {
		t.Errorf("LangLabel = %q, want the file counts", got)
	}

	// With no histogram it falls back to the languages, then to the primary.
	if got := (Repo{Langs: []string{"Python", "Shell"}}).LangLabel(); got != "Python, Shell" {
		t.Errorf("fallback = %q", got)
	}
	if got := (Repo{Language: "Python"}).LangLabel(); got != "Python" {
		t.Errorf("fallback = %q", got)
	}
	if got := (Repo{}).LangLabel(); got != "-" {
		t.Errorf("empty = %q, want a dash", got)
	}
}

func TestGeneratedFilesAreSkipped(t *testing.T) {
	// Every one of these is valid UTF-8, so only the content rules catch them.
	data := makeTarball(t, "w", map[string][]byte{
		"README.md":                        []byte(strings.Repeat("Notes on the pedal firmware.\n\n", 20)),
		"src/main.c":                       []byte(strings.Repeat("int step(int x) {\n\treturn x + 1;\n}\n\n", 40)),
		"Hardware/rotary/rotary.kicad_sch": []byte(kicadLike(3000)),
		"package-lock.json":                []byte(`{"lockfileVersion": 3}`),
		"go.sum":                           []byte("example.com/x v1.0.0 h1:abc=\n"),
		"vendor/dep/dep.go":                []byte(authoredGo(10)),
	})

	b, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}

	var kept []string
	for _, f := range b.Files {
		kept = append(kept, f.Path)
	}
	if strings.Join(kept, ",") != "README.md,src/main.c" {
		t.Errorf("kept %v, want only the authored files", kept)
	}
	if len(b.Skipped) != 4 {
		t.Errorf("skipped %d, want 4: %+v", len(b.Skipped), b.Skipped)
	}

	all, err := ReadTarball(data, ReadOptions{MaxBytes: 1 << 20, IncludeGenerated: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Files) != 6 {
		t.Errorf("-include-generated kept %d files, want all 6", len(all.Files))
	}
}
