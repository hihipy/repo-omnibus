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
	doc := Render([]string{"hihipy"}, []Bundle{b}, time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))

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
	}}, timeFixed())

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
