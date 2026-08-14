package omnibus

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// This is the arrow-key picker. It does what fzf and the Charm libraries do,
// with the standard library: put the terminal in raw mode so keys arrive
// unbuffered, read escape sequences, and redraw the list in place.
//
// Raw mode goes through stty rather than a terminal package, which keeps the
// tool dependency-free. When stty is missing or stdin is not a terminal, Pick
// falls back to the typed prompt in pick.go.

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyToggle
	keyAll
	keyNone_
	keyAccept
	keyCancel
	keyTop
	keyBottom
)

// decodeKey reads one keypress from the head of buf. It returns the key and how
// many bytes it consumed, or 0 when buf holds an incomplete escape sequence.
func decodeKey(buf []byte) (key, int) {
	if len(buf) == 0 {
		return keyNone, 0
	}
	switch buf[0] {
	case '\r', '\n':
		return keyAccept, 1
	case ' ':
		return keyToggle, 1
	case 'a', 'A':
		return keyAll, 1
	case 'n', 'N':
		return keyNone_, 1
	case 'k':
		return keyUp, 1
	case 'j':
		return keyDown, 1
	case 'g':
		return keyTop, 1
	case 'G':
		return keyBottom, 1
	case 'q', 3: // q or ctrl-c
		return keyCancel, 1
	case 0x1b:
		if len(buf) == 1 {
			return keyCancel, 1 // a lone escape
		}
		if len(buf) < 3 {
			return keyNone, 0 // wait for the rest of the sequence
		}
		if buf[1] == '[' {
			switch buf[2] {
			case 'A':
				return keyUp, 3
			case 'B':
				return keyDown, 3
			case 'H':
				return keyTop, 3
			case 'F':
				return keyBottom, 3
			}
		}
		return keyNone, 3 // some other escape sequence, ignored
	}
	return keyNone, 1
}

// pickState is the picker's model, kept separate from the terminal so the
// behaviour can be tested without one.
type pickState struct {
	repos    []Repo
	selected []bool
	cursor   int
	top      int // first visible row, for lists taller than the window
	height   int // visible rows
	width    int // visible columns
}

func newPickState(repos []Repo, height, width int) *pickState {
	sel := make([]bool, len(repos))
	for i := range sel {
		sel[i] = true // everything starts selected, so the common case is Enter
	}
	if height < 3 {
		height = len(repos)
	}
	if width < 40 {
		width = 100
	}
	return &pickState{repos: repos, selected: sel, height: height, width: width}
}

// apply advances the state by one keypress and reports whether the picker is
// finished and whether it was accepted.
func (s *pickState) apply(k key) (done, accepted bool) {
	switch k {
	case keyUp:
		if s.cursor > 0 {
			s.cursor--
		}
	case keyDown:
		if s.cursor < len(s.repos)-1 {
			s.cursor++
		}
	case keyTop:
		s.cursor = 0
	case keyBottom:
		s.cursor = len(s.repos) - 1
	case keyToggle:
		s.selected[s.cursor] = !s.selected[s.cursor]
	case keyAll:
		for i := range s.selected {
			s.selected[i] = true
		}
	case keyNone_:
		for i := range s.selected {
			s.selected[i] = false
		}
	case keyAccept:
		return true, true
	case keyCancel:
		return true, false
	}
	s.scroll()
	return false, false
}

// scroll keeps the cursor inside the visible window.
func (s *pickState) scroll() {
	if s.cursor < s.top {
		s.top = s.cursor
	}
	if s.cursor >= s.top+s.height {
		s.top = s.cursor - s.height + 1
	}
	if s.top < 0 {
		s.top = 0
	}
}

func (s *pickState) chosen() []Repo {
	var out []Repo
	for i, r := range s.repos {
		if s.selected[i] {
			out = append(out, r)
		}
	}
	return out
}

// layout decides how wide each column may be, given the window. Columns are
// dropped rather than wrapped: a wrapped row would also desynchronise the
// redraw, which moves the cursor up by one line per row.
type layout struct{ name, desc, lang, size int }

func (s *pickState) layout() layout {
	var l layout
	langWidth := 0
	for _, r := range s.repos {
		if n := len(r.Name); n > l.name {
			l.name = n
		}
		if n := len(r.LangLabel()); n > langWidth {
			langWidth = n
		}
		if n := len(commas(int64(r.Size))) + 3; n > l.size {
			l.size = n
		}
	}

	// A very long name cannot be allowed to consume the whole row.
	if cap := s.width/2 - 6; l.name > cap && cap > 12 {
		l.name = cap
	}

	// Languages come after the description, so the description is budgeted
	// first and the language column takes what is left, up to what it needs.
	rest := s.width - 6 - l.name - 2 - l.size
	if rest >= 18 {
		l.desc = rest / 2
		if l.desc > 60 {
			l.desc = 60
		}
		rest -= l.desc + 2
	}
	if rest >= 6 {
		l.lang = rest - 2
		if l.lang > langWidth {
			l.lang = langWidth
		}
	}
	return l
}

// view renders the current frame as lines, without any cursor movement, so it
// can be compared in a test.
func (s *pickState) view() []string {
	l := s.layout()

	count, kb := 0, 0
	for i, r := range s.repos {
		if s.selected[i] {
			count++
			kb += r.Size
		}
	}

	var lines []string
	end := s.top + s.height
	if end > len(s.repos) {
		end = len(s.repos)
	}
	for i := s.top; i < end; i++ {
		r := s.repos[i]
		mark := " "
		if s.selected[i] {
			mark = "x"
		}
		point := "  "
		if i == s.cursor {
			point = "> "
		}

		line := fmt.Sprintf("%s[%s] %-*s", point, mark, l.name, truncate(r.Name, l.name))
		if l.desc > 0 {
			line += fmt.Sprintf("  %-*s", l.desc, truncate(r.Description, l.desc))
		}
		if l.lang > 0 {
			line += fmt.Sprintf("  %-*s", l.lang, truncate(r.LangLabel(), l.lang))
		}
		line += fmt.Sprintf("  %*s KB", l.size-3, commas(int64(r.Size)))
		lines = append(lines, strings.TrimRight(line, " "))
	}

	if s.top > 0 || end < len(s.repos) {
		lines = append(lines, fmt.Sprintf("      showing %d to %d of %d",
			s.top+1, end, len(s.repos)))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %d of %d selected, %s KB",
		count, len(s.repos), commas(int64(kb))))
	lines = append(lines, truncate(
		"  up and down to move, space to toggle, a all, n none, enter to confirm, q to cancel",
		s.width))
	return lines
}

// stty runs stty against the controlling terminal and returns its output, which
// is how raw mode is entered and left without a terminal package.
func stty(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// termSize asks stty for the window, so a long list scrolls instead of
// overflowing and a wide row is trimmed instead of wrapping. A wrapped row also
// breaks the redraw, which moves the cursor up by one line per row.
func termSize(tty *os.File) (rows, cols int) {
	out, err := stty(tty, "size")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0
	}
	rows, _ = strconv.Atoi(fields[0])
	cols, _ = strconv.Atoi(fields[1])
	return rows, cols
}

// truncate shortens a string to n columns, marking the cut.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "\u2026"
	}
	return string(r[:n-1]) + "\u2026"
}

// PickArrows runs the arrow-key picker against the controlling terminal. It
// returns ok=false when the terminal cannot be driven, so the caller can fall
// back to the typed prompt.
func PickArrows(repos []Repo, out io.Writer) (picked []Repo, ok bool, err error) {
	if len(repos) == 0 {
		return nil, true, nil
	}

	unavailable := func(why string) {
		fmt.Fprintf(out, "arrow picker unavailable (%s), falling back to typing\n", why)
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		unavailable("no /dev/tty")
		return nil, false, nil
	}
	defer tty.Close()

	saved, err := stty(tty, "-g")
	if err != nil {
		unavailable("stty -g: " + err.Error())
		return nil, false, nil
	}
	if _, err := stty(tty, "raw", "-echo"); err != nil {
		unavailable("stty raw: " + err.Error())
		return nil, false, nil
	}
	restore := func() {
		stty(tty, saved)
		fmt.Fprint(out, "\x1b[?25h") // show the cursor again
	}
	defer restore()
	fmt.Fprint(out, "\x1b[?25l") // hide the cursor while redrawing

	// Leave room for the footer and a little breathing space.
	rows, cols := termSize(tty)
	window := rows - 6
	if window > len(repos) {
		window = len(repos)
	}
	s := newPickState(repos, window, cols)

	drawn := 0
	draw := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dA", drawn)
		}
		lines := s.view()
		for _, l := range lines {
			fmt.Fprint(out, "\r\x1b[K", l, "\r\n")
		}
		drawn = len(lines)
	}
	draw()

	var buf []byte
	chunk := make([]byte, 64)
	for {
		n, readErr := tty.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if readErr != nil && n == 0 {
			return nil, true, fmt.Errorf("reading keys: %w", readErr)
		}

		for len(buf) > 0 {
			k, used := decodeKey(buf)
			if used == 0 {
				break // incomplete escape sequence, wait for more
			}
			buf = buf[used:]

			done, accepted := s.apply(k)
			if done {
				restore()
				if !accepted {
					return nil, true, nil
				}
				return s.chosen(), true, nil
			}
		}
		draw()
	}
}
