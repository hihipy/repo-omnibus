package omnibus

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Pick asks which repositories to collect and returns the chosen subset.
//
// The prompt accepts an empty line for everything, or a list of entries to
// take. Each entry is a number, a range, a repository name, or a glob. Putting
// a minus in front of every entry inverts the whole list into a drop. Taking
// and dropping cannot be mixed, because "1,-3" reads as either "just 1, also
// not 3" or "1 through 3" and neither reading is safe to guess.
func Pick(repos []Repo, in io.Reader, out io.Writer) ([]Repo, error) {
	if len(repos) == 0 {
		return nil, nil
	}

	// Prefer the arrow-key picker, and fall back to typing when the terminal
	// cannot be driven.
	if in == os.Stdin {
		if picked, ok, err := PickArrows(repos, out); ok {
			if err != nil {
				return nil, err
			}
			if picked == nil {
				return nil, nil // cancelled
			}
			fmt.Fprintf(out, "\nselected %d of %d repositories\n", len(picked), len(repos))
			return picked, nil
		}
	}

	// The typed listing reuses the picker's layout, so it cannot wrap either.
	width := 100
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		if _, cols := termSize(tty); cols > 0 {
			width = cols
		}
		tty.Close()
	}
	st := newPickState(repos, len(repos), width)
	numWidth := len(strconv.Itoa(len(repos)))

	fmt.Fprintln(out)
	for i, line := range st.view()[:len(repos)] {
		// Swap the checkbox for the number the prompt asks for.
		fmt.Fprintf(out, "  %*d %s\n", numWidth, i+1, strings.TrimPrefix(line, "  [x] "))
	}
	fmt.Fprintln(out, "\nEnter for all. Take with numbers, names, or globs (1,4,7-9 or sql-x-ray).")
	fmt.Fprintln(out, "Put a minus on every entry to drop instead (-12 or -*.github.io).")

	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "> ")
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("reading selection: %w", err)
		}

		chosen, perr := ParseSelection(line, names(repos))
		if perr != nil {
			fmt.Fprintf(out, "%v\n", perr)
			continue
		}
		if len(chosen) == 0 {
			fmt.Fprintln(out, "that leaves nothing to collect")
			continue
		}

		picked := make([]Repo, 0, len(chosen))
		for _, i := range chosen {
			picked = append(picked, repos[i-1])
		}
		fmt.Fprintf(out, "\nselected %d of %d repositories\n", len(picked), len(repos))
		return picked, nil
	}
}

// ParseSelection turns a line of user input into a sorted list of 1-based
// indexes into names. An empty line means everything. Entries may be numbers,
// ranges, repository names, or globs, and a leading minus on every entry
// inverts the whole selection into a drop.
func ParseSelection(line string, names []string) ([]int, error) {
	n := len(names)
	line = strings.TrimSpace(line)
	if line == "" {
		return seq(n), nil
	}

	fields := strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return seq(n), nil
	}

	dropping := strings.HasPrefix(fields[0], "-")
	picked := map[int]bool{}
	for _, f := range fields {
		if strings.HasPrefix(f, "-") != dropping {
			return nil, fmt.Errorf("mixing take and drop is ambiguous: %q", line)
		}
		f = strings.TrimPrefix(f, "-")

		if lo, hi, err := parseRange(f, n); err == nil {
			for i := lo; i <= hi; i++ {
				picked[i] = true
			}
			continue
		} else if isNumeric(f) {
			// It parsed as digits but fell outside the list, which is a
			// mistake worth reporting rather than treating as a name.
			return nil, err
		}

		matched := false
		for i, name := range names {
			if strings.EqualFold(name, f) {
				picked[i+1] = true
				matched = true
				continue
			}
			if ok, err := path.Match(strings.ToLower(f), strings.ToLower(name)); err == nil && ok {
				picked[i+1] = true
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("no repository matches %q", f)
		}
	}

	var out []int
	for i := 1; i <= n; i++ {
		if picked[i] != dropping {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out, nil
}

// parseRange reads "7" or "4-9" and bounds it to the list.
func parseRange(f string, n int) (int, int, error) {
	lo, hi := f, f
	if i := strings.Index(f, "-"); i > 0 {
		lo, hi = f[:i], f[i+1:]
	}

	a, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return 0, 0, fmt.Errorf("not a number: %q", f)
	}
	b, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return 0, 0, fmt.Errorf("not a number: %q", f)
	}
	if a > b {
		a, b = b, a
	}
	if a < 1 || b > n {
		return 0, 0, fmt.Errorf("out of range 1 to %d: %q", n, f)
	}
	return a, b, nil
}

// isNumeric reports whether every character could belong to a number or range,
// which separates a mistyped index from a repository name.
func isNumeric(f string) bool {
	for _, r := range f {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return f != ""
}

func names(repos []Repo) []string {
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.Name
	}
	return out
}

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// interactive reports whether stdin is a terminal, so -pick can fail early in a
// pipeline instead of hanging on a prompt nobody will answer.
// stdinIsTerminal is a variable so a test can stand where a pipeline stands.
// Under `go test` the real check can go either way depending on how the runner
// was invoked, and the tool's behaviour should not depend on that.
var stdinIsTerminal = interactive

func interactive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
