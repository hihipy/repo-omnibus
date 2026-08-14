package omnibus

import (
	"strings"
	"testing"
)

// skippedBundles stands in for a run that dropped several kinds of file.
func skippedBundles() []Bundle {
	a := Repo{Name: "timeline", HTMLURL: "https://github.com/hihipy/timeline", DefaultBranch: "main"}
	b := Repo{Name: "tabs-2-json", HTMLURL: "https://github.com/hihipy/tabs-2-json", DefaultBranch: "main"}
	return []Bundle{
		{
			Repo:  a,
			Files: []File{{Path: "README.md", Text: "# hi\n"}},
			Skipped: []Skipped{
				{Path: "timeline.xlsx", Reason: "a binary file", Detail: ".xlsx", Size: 13600},
			},
		},
		{
			Repo:  b,
			Files: []File{{Path: "README.md", Text: "# hi\n"}},
			Skipped: []Skipped{
				{Path: "src/icons/icon128.png", Reason: "a binary file", Detail: ".png", Size: 2_400_000},
				{Path: "src/icons/icon48.png", Reason: "a binary file", Detail: ".png", Size: 4096},
				{Path: "src/lib/bundle.js", Reason: "a serialized blob",
					Detail: "one line of 40,000 characters", Size: 51200},
				{Path: "LICENSE.md", Reason: "license text, identical in every repository", Size: 19000},
			},
		},
	}
}

func TestSkipStatsGroupsByReason(t *testing.T) {
	stats := SkipStats(skippedBundles())
	if len(stats) != 3 {
		t.Fatalf("got %d reasons, want 3: %+v", len(stats), stats)
	}
	// Every binary counts under one heading whatever its extension, so a run
	// over a large account reports in lines rather than screens.
	if stats[0].Reason != "a binary file" || stats[0].Count != 3 {
		t.Errorf("first group = %+v, want the three binaries first", stats[0])
	}
	if stats[0].Bytes != 2_417_696 {
		t.Errorf("binary bytes = %d, want 2,417,696", stats[0].Bytes)
	}
}

func TestSkipEntriesLargestFirst(t *testing.T) {
	entries := SkipEntries(skippedBundles())
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5", len(entries))
	}
	if entries[0].Path != "src/icons/icon128.png" {
		t.Errorf("first entry = %s, want the largest file", entries[0].Path)
	}
	if entries[0].Repo.Name != "tabs-2-json" {
		t.Errorf("entry lost its repository: %+v", entries[0])
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:               "512 bytes",
		13600:             "13 KB",
		2_400_000:         "2.3 MB",
		16_492_672 * 1024: "15.7 GB", // simonw/vaccinespotter-history
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTerminalSummary(t *testing.T) {
	got := TerminalSummary(skippedBundles(), 8)
	for _, want := range []string{
		"2 repositories",
		"Not included: 5 files",
		"a binary file",
		"Download these from GitHub",
		"tabs-2-json/src/icons/icon128.png",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q, got:\n%s", want, got)
		}
	}
}

func TestTerminalSummaryCapsTheList(t *testing.T) {
	got := TerminalSummary(skippedBundles(), 2)
	if !strings.Contains(got, "and 2 more") {
		t.Errorf("summary should say how many it withheld, got:\n%s", got)
	}
}

func TestTerminalSummaryWhenNothingSkipped(t *testing.T) {
	clean := []Bundle{{Repo: Repo{Name: "r1"}, Files: []File{{Path: "a.go", Text: "x"}}}}
	got := TerminalSummary(clean, 8)
	if !strings.Contains(got, "No files were left out") {
		t.Errorf("summary should say so plainly, got:\n%s", got)
	}
}

func TestRenderCarriesNotIncluded(t *testing.T) {
	doc := Render("hihipy", skippedBundles(), timeFixed())
	for _, want := range []string{
		"## Not Included",
		"5 files could not be carried as text",
		"[`timeline.xlsx`](https://github.com/hihipy/timeline/blob/main/timeline.xlsx)",
		"| a binary file: .xlsx | 13 KB |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document missing %q", want)
		}
	}
	// The section belongs above the repositories, not buried after them.
	if strings.Index(doc, "## Not Included") > strings.Index(doc, "# timeline") {
		t.Error("Not Included should come before the repository sections")
	}
}

func TestTypeTotals(t *testing.T) {
	bundles := []Bundle{
		{Repo: Repo{Name: "a"}, Files: []File{
			{Path: "main.py", Text: strings.Repeat("x", 3000)},
			{Path: "README.md", Text: strings.Repeat("x", 500)},
			{Path: "Dockerfile", Text: strings.Repeat("x", 100)},
		}},
		{Repo: Repo{Name: "b"}, Files: []File{
			{Path: "util.py", Text: strings.Repeat("x", 1000)},
			{Path: "q.sql", Text: strings.Repeat("x", 2000)},
		}},
	}
	got := TypeTotals(bundles)

	if got[0].Ext != ".py" || got[0].Files != 2 || got[0].Chars != 4000 {
		t.Errorf("first = %+v, want .py with 2 files and 4000 characters", got[0])
	}
	if got[1].Ext != ".sql" {
		t.Errorf("second = %+v, want .sql, the next heaviest", got[1])
	}
	// A file with no extension is reported by name, since Dockerfile is a type.
	var sawDockerfile bool
	for _, ty := range got {
		if ty.Ext == "Dockerfile" {
			sawDockerfile = true
		}
	}
	if !sawDockerfile {
		t.Errorf("extensionless files should be named, got %+v", got)
	}
}

func TestByWeight(t *testing.T) {
	bundles := []Bundle{
		{Repo: Repo{Name: "small"}, Files: []File{{Path: "a.py", Text: "x"}}},
		{Repo: Repo{Name: "large"}, Files: []File{{Path: "b.py", Text: strings.Repeat("x", 500)}}},
	}
	got := ByWeight(bundles)
	if got[0].Repo.Name != "large" {
		t.Errorf("order = %s first, want the heaviest", got[0].Repo.Name)
	}
	if bundles[0].Repo.Name != "small" {
		t.Error("ByWeight should not reorder its input")
	}
}

func TestTerminalSummaryShowsCompositionAndWeight(t *testing.T) {
	got := TerminalSummary(skippedBundles(), 8)
	for _, want := range []string{
		"What it is made of:",
		"Where the context goes:",
		".md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q, got:\n%s", want, got)
		}
	}
}
