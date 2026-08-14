package omnibus

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// fiveNames stands in for a repository list in the selection tests.
var fiveNames = []string{"alpha", "beta", "gamma", "hihipy.github.io", "sql-x-ray"}

func TestParseSelection(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want []int
	}{
		{"", 4, []int{1, 2, 3, 4}},
		{"   ", 3, []int{1, 2, 3}},
		{"2", 4, []int{2}},
		{"1,3", 4, []int{1, 3}},
		{"2-4", 5, []int{2, 3, 4}},
		{"1, 4-5", 5, []int{1, 4, 5}},
		{"4-2", 5, []int{2, 3, 4}},   // reversed range still reads left to right
		{"1 3", 4, []int{1, 3}},      // spaces separate as well as commas
		{"2,2,2", 3, []int{2}},       // repeats collapse
		{"-2", 4, []int{1, 3, 4}},    // drop one
		{"-2,-4", 5, []int{1, 3, 5}}, // drop several
		{"-2-4", 5, []int{1, 5}},     // drop a range
	}
	for _, c := range cases {
		got, err := ParseSelection(c.in, fiveNames[:c.n])
		if err != nil {
			t.Errorf("ParseSelection(%q, %d) errored: %v", c.in, c.n, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("ParseSelection(%q, %d) = %v, want %v", c.in, c.n, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseSelection(%q, %d) = %v, want %v", c.in, c.n, got, c.want)
				break
			}
		}
	}
}

func TestParseSelectionRejects(t *testing.T) {
	for _, in := range []string{
		"1,-3", // mixing take and drop
		"0",    // below the list
		"9",    // above the list
		"1-9",  // range runs off the end
		"two",  // not a number
		"1,,x", // still not a number
	} {
		if got, err := ParseSelection(in, fiveNames); err == nil {
			t.Errorf("ParseSelection(%q, 5) = %v, want an error", in, got)
		}
	}
}

func TestParseSelectionByName(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"sql-x-ray", []int{5}},
		{"SQL-X-RAY", []int{5}}, // names match case-insensitively
		{"alpha,gamma", []int{1, 3}},
		{"-hihipy.github.io", []int{1, 2, 3, 5}}, // drop by name
		{"-*.github.io", []int{1, 2, 3, 5}},      // drop by glob
		{"*a*", []int{1, 2, 3, 5}},               // glob matches anywhere in the name
		{"g*", []int{3}},                         // anchored glob
		{"2,sql-x-ray", []int{2, 5}},             // numbers and names together
	}
	for _, c := range cases {
		got, err := ParseSelection(c.in, fiveNames)
		if err != nil {
			t.Errorf("ParseSelection(%q) errored: %v", c.in, err)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("ParseSelection(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPickDropsAndReprompts(t *testing.T) {
	repos := testRepos(4)
	// First line is invalid, so the picker should explain and ask again.
	in := strings.NewReader("1,-3\n-2\n")
	var out bytes.Buffer

	picked, err := Pick(repos, in, &out)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	var names []string
	for _, r := range picked {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "r1,r3,r4" {
		t.Errorf("picked %v, want r1,r3,r4", names)
	}
	if !strings.Contains(out.String(), "ambiguous") {
		t.Error("expected the invalid line to be explained")
	}
}

func TestPickEmptyLineTakesAll(t *testing.T) {
	repos := testRepos(3)
	picked, err := Pick(repos, strings.NewReader("\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if len(picked) != 3 {
		t.Errorf("picked %d, want 3", len(picked))
	}
}

func TestPickListsSizes(t *testing.T) {
	var out bytes.Buffer
	repos := testRepos(2)
	repos[0].Size = 29920
	if _, err := Pick(repos, strings.NewReader("\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "29,920 KB") {
		t.Errorf("listing should show sizes, got:\n%s", out.String())
	}
}
