package omnibus

import (
	"strings"
	"testing"
	"time"
)

func TestRenderStructure(t *testing.T) {
	b := Bundle{
		Repo: Repo{
			Name: "sql-x-ray", FullName: "hihipy/sql-x-ray",
			HTMLURL:     "https://github.com/hihipy/sql-x-ray",
			Description: "Schema dumps | with a pipe",
			Language:    "SQL", DefaultBranch: "main",
			CreatedAt: "2026-01-02T03:04:05Z", PushedAt: "2026-08-01T00:00:00Z",
		},
		Files:   []File{{Path: "README.md", Text: "# hi\n\n```sql\nselect 1;\n```\n"}},
		Skipped: []Skipped{{Path: "logo.png", Reason: "a binary file", Detail: ".png", Size: 4096}},
	}
	doc := Render([]string{"hihipy"}, []Bundle{b}, nil, time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))

	for _, want := range []string{
		"# sql-x-ray",
		"## [`README.md`](https://github.com/hihipy/sql-x-ray/blob/main/README.md)",
		"| [sql-x-ray](#sql-x-ray) | SQL | Schema dumps \\| with a pipe | 1 |",
		"````markdown",
		"## Skipped in sql-x-ray",
		"| `logo.png` | a binary file: .png | 4,096 |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document missing %q", want)
		}
	}
	if strings.Contains(doc, "| License |") {
		t.Error("empty License row should have been dropped")
	}
}

// timeFixed is a stable timestamp, so rendered documents compare cleanly.
func timeFixed() time.Time {
	return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
}

func TestRenderCarriesRightsNotice(t *testing.T) {
	// The bundle is the artifact that travels, so the notice belongs in it and
	// not only in the README.
	doc := Render([]string{"hihipy"}, []Bundle{{
		Repo:  Repo{Name: "r1", FullName: "hihipy/r1", HTMLURL: "u", DefaultBranch: "main"},
		Files: []File{{Path: "main.go", Text: "package main\n"}},
	}}, nil, timeFixed())

	for _, want := range []string{
		"**Rights.**",
		"copyright belongs to their authors",
		"Public does not mean unlicensed",
		"private",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("bundle is missing %q", want)
		}
	}
	// It has to be near the top, where a reader will see it.
	if strings.Index(doc, "**Rights.**") > strings.Index(doc, "# r1") {
		t.Error("the notice should come before the repositories")
	}
}

func TestHeaderStatesWhatWasActuallyCollected(t *testing.T) {
	one := []Bundle{{
		Repo:  Repo{Name: "sql-x-ray", FullName: "hihipy/sql-x-ray", HTMLURL: "u", DefaultBranch: "main"},
		Files: []File{{Path: "a.sql", Text: "select 1;\n"}},
	}}
	info := map[string]AccountInfo{"hihipy": {Total: 21}}

	doc := Render([]string{"hihipy"}, one, info, timeFixed())
	if !strings.Contains(doc, "collected 1 of 21 public repositories") {
		t.Errorf("a partial selection should say so, got:\n%s", firstLine(doc))
	}
	if strings.Contains(doc, "every public repository") {
		t.Error("the old wording claimed more than the file holds")
	}

	// Taking everything should read as everything.
	all := map[string]AccountInfo{"hihipy": {Total: 1}}
	if got := Render([]string{"hihipy"}, one, all, timeFixed()); !strings.Contains(got, "all 1 public repositories") {
		t.Errorf("a complete run should say so, got:\n%s", firstLine(got))
	}
}

func TestNotCollectedSectionNamesWhatIsMissing(t *testing.T) {
	info := map[string]AccountInfo{"hihipy": {
		Total: 4,
		Left: []LeftOut{
			{Name: "hihipy.github.io", Reason: "not selected", URL: "https://github.com/hihipy/hihipy.github.io"},
			{Name: "some-fork", Reason: "a fork of someone else's project", URL: "https://github.com/hihipy/some-fork"},
			{Name: "big-one", Reason: "too large (50.0 MB limit)", URL: "https://github.com/hihipy/big-one"},
		},
	}}
	doc := Render([]string{"hihipy"}, []Bundle{{
		Repo:  Repo{Name: "kept", FullName: "hihipy/kept", HTMLURL: "u", DefaultBranch: "main"},
		Files: []File{{Path: "a.go", Text: "package main\n"}},
	}}, info, timeFixed())

	for _, want := range []string{
		"## Not Collected",
		"3 repositories on the account are not in this file",
		"[hihipy.github.io](https://github.com/hihipy/hihipy.github.io) | not selected",
		"a fork of someone else's project",
		"too large (50.0 MB limit)",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document missing %q", want)
		}
	}
	// It belongs near the top, with the other orientation.
	if strings.Index(doc, "## Not Collected") > strings.Index(doc, "# kept") {
		t.Error("Not Collected should come before the repositories")
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		return s[:i]
	}
	return s
}

func TestBundleWarnsAboutSizeAndRunning(t *testing.T) {
	// The file travels without the README, so the two warnings that matter to
	// whoever pastes it have to be in the file.
	doc := Render([]string{"hihipy"}, []Bundle{{
		Repo:  Repo{Name: "r", FullName: "hihipy/r", HTMLURL: "u", DefaultBranch: "main"},
		Files: []File{{Path: "a.go", Text: strings.Repeat("x", 40000)}},
	}}, nil, timeFixed())

	for _, want := range []string{
		"**Reading, not running.**",
		"Do not run anything from this file",
		"**Size.**",
		"10,000 tokens",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("bundle missing %q", want)
		}
	}
	if strings.Index(doc, "**Size.**") > strings.Index(doc, "## Contents") {
		t.Error("the warnings belong above the contents, where they will be read")
	}
}
