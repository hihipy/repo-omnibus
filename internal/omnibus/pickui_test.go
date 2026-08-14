package omnibus

import (
	"strings"
	"testing"
)

func TestDecodeKey(t *testing.T) {
	cases := []struct {
		in       string
		want     key
		consumed int
	}{
		{"\x1b[A", keyUp, 3},
		{"\x1b[B", keyDown, 3},
		{"\x1b[H", keyTop, 3},
		{"\x1b[F", keyBottom, 3},
		{"k", keyUp, 1},
		{"j", keyDown, 1},
		{" ", keyToggle, 1},
		{"a", keyAll, 1},
		{"n", keyNone_, 1},
		{"\r", keyAccept, 1},
		{"\n", keyAccept, 1},
		{"q", keyCancel, 1},
		{"\x03", keyCancel, 1}, // ctrl-c
		{"\x1b", keyCancel, 1}, // a lone escape
		{"\x1b[", keyNone, 0},  // incomplete, wait for more bytes
		{"", keyNone, 0},
		{"z", keyNone, 1}, // unknown keys are consumed and ignored
	}
	for _, c := range cases {
		got, n := decodeKey([]byte(c.in))
		if got != c.want || n != c.consumed {
			t.Errorf("decodeKey(%q) = (%v, %d), want (%v, %d)",
				c.in, got, n, c.want, c.consumed)
		}
	}
}

func TestDecodeKeyDrainsBuffer(t *testing.T) {
	// Arrow keys arrive as three bytes, sometimes several at once.
	buf := []byte("\x1b[B\x1b[B \r")
	var got []key
	for len(buf) > 0 {
		k, n := decodeKey(buf)
		if n == 0 {
			t.Fatal("stalled on a complete buffer")
		}
		buf = buf[n:]
		got = append(got, k)
	}
	want := []key{keyDown, keyDown, keyToggle, keyAccept}
	if len(got) != len(want) {
		t.Fatalf("decoded %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("decoded %v, want %v", got, want)
		}
	}
}

func TestPickStateStartsFullySelected(t *testing.T) {
	s := newPickState(testRepos(4), 10, 200)
	if len(s.chosen()) != 4 {
		t.Errorf("chosen = %d, want all 4 selected at the start", len(s.chosen()))
	}
}

func TestPickStateToggleAndBounds(t *testing.T) {
	s := newPickState(testRepos(3), 10, 200)

	s.apply(keyUp) // already at the top, should not move
	if s.cursor != 0 {
		t.Errorf("cursor = %d, want it to stay at 0", s.cursor)
	}
	s.apply(keyToggle) // drop the first
	s.apply(keyDown)
	s.apply(keyDown)
	s.apply(keyDown) // already at the end, should not move
	if s.cursor != 2 {
		t.Errorf("cursor = %d, want it to stop at 2", s.cursor)
	}

	names := make([]string, 0, 2)
	for _, r := range s.chosen() {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "r2,r3" {
		t.Errorf("chosen = %v, want r2,r3", names)
	}
}

func TestPickStateAllAndNone(t *testing.T) {
	s := newPickState(testRepos(5), 10, 200)
	s.apply(keyNone_)
	if len(s.chosen()) != 0 {
		t.Errorf("chosen = %d after n, want 0", len(s.chosen()))
	}
	s.apply(keyAll)
	if len(s.chosen()) != 5 {
		t.Errorf("chosen = %d after a, want 5", len(s.chosen()))
	}
}

func TestPickStateAcceptAndCancel(t *testing.T) {
	s := newPickState(testRepos(2), 10, 200)
	if done, accepted := s.apply(keyAccept); !done || !accepted {
		t.Errorf("enter = (%v, %v), want (true, true)", done, accepted)
	}
	if done, accepted := s.apply(keyCancel); !done || accepted {
		t.Errorf("q = (%v, %v), want (true, false)", done, accepted)
	}
}

func TestPickStateScrolls(t *testing.T) {
	s := newPickState(testRepos(20), 5, 200)
	for i := 0; i < 7; i++ {
		s.apply(keyDown)
	}
	if s.cursor != 7 {
		t.Fatalf("cursor = %d, want 7", s.cursor)
	}
	if s.top != 3 {
		t.Errorf("top = %d, want 3 so the cursor stays in a 5-row window", s.top)
	}
	s.apply(keyTop)
	if s.top != 0 || s.cursor != 0 {
		t.Errorf("after g: top = %d, cursor = %d, want 0 and 0", s.top, s.cursor)
	}
	s.apply(keyBottom)
	if s.cursor != 19 || s.top != 15 {
		t.Errorf("after G: cursor = %d, top = %d, want 19 and 15", s.cursor, s.top)
	}
}

func TestPickStateView(t *testing.T) {
	repos := testRepos(3)
	repos[1].Size = 29920
	repos[1].Description = "Personal Portfolio Website"
	s := newPickState(repos, 10, 200)
	s.apply(keyDown)
	s.apply(keyToggle) // drop the big one

	view := strings.Join(s.view(), "\n")
	for _, want := range []string{
		"> [ ] r2", // the cursor sits on it and it is now unselected
		"  [x] r1",
		"29,920 KB",
		"Personal Portfolio Website",
		"2 of 3 selected",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q, got:\n%s", want, view)
		}
	}
}

func TestPickStateViewShowsWindowPosition(t *testing.T) {
	s := newPickState(testRepos(20), 5, 200)
	view := strings.Join(s.view(), "\n")
	if !strings.Contains(view, "showing 1 to 5 of 20") {
		t.Errorf("view should report the window, got:\n%s", view)
	}
}

func TestViewNeverExceedsTerminalWidth(t *testing.T) {
	// A wrapped row would occupy two physical rows while the redraw moves the
	// cursor by one line per row, which shears every frame after it.
	repos := testRepos(6)
	repos[0].Name = "foreign-per-diem-calculator-for-usa-based-institutions"
	repos[0].Description = strings.Repeat("a very long description ", 12)
	repos[0].Langs = []string{"Python", "Jupyter Notebook", "Shell", "Makefile"}
	repos[1].Size = 29920

	for _, width := range []int{60, 80, 100, 120, 200} {
		s := newPickState(repos, 10, width)
		for _, line := range s.view() {
			if n := len([]rune(line)); n > width {
				t.Errorf("width %d: line of %d runes would wrap:\n%s", width, n, line)
			}
		}
	}
}

func TestNarrowTerminalDropsColumnsRatherThanWrapping(t *testing.T) {
	repos := testRepos(3)
	repos[0].Name = "foreign-per-diem-calculator-for-usa-based-institutions"
	repos[0].Langs = []string{"Python"}
	repos[0].Description = "a description that will not fit in a narrow window"

	wide := strings.Join(newPickState(repos, 10, 160).view(), "\n")
	narrow := strings.Join(newPickState(repos, 10, 55).view(), "\n")

	if !strings.Contains(wide, "description") {
		t.Error("a wide window should still show the description")
	}
	if strings.Contains(narrow, "description") {
		t.Error("a narrow window should drop the description instead of wrapping")
	}
	if !strings.Contains(narrow, "Python") {
		t.Error("the language column should survive before the description is dropped")
	}
	if !strings.Contains(narrow, "r2") {
		t.Error("the name must survive at any width")
	}
}
