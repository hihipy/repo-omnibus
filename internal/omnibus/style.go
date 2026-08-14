package omnibus

import (
	"os"
	"strings"
)

// A run prints a lot of lines, and the ones that matter are the ones a reader
// skims past: an account that produced nothing, a file that was written. Colour
// is how a terminal says "look here".
//
// It stays off unless stdout is a terminal, so a redirected run produces clean
// text, and it honours NO_COLOR, which is the convention for turning this off
// everywhere at once.

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

// styled is a variable so a test can turn styling on without a terminal and off
// without redirecting.
var styled = colourSupported()

func colourSupported() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if term := os.Getenv("TERM"); term == "" || term == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func paint(codes, s string) string {
	if !styled || s == "" {
		return s
	}
	// Reset before any newline so a highlighted line does not colour the rest
	// of the screen when it wraps.
	return codes + strings.ReplaceAll(s, "\n", ansiReset+"\n"+codes) + ansiReset
}

// Bold marks a heading in the run's report.
func Bold(s string) string { return paint(ansiBold, s) }

// Notice marks something the reader needs to see and might otherwise skim past,
// such as an account that produced no file.
func Notice(s string) string { return paint(ansiBold+ansiYellow, s) }

// Good marks a finished piece of work, such as a file written.
func Good(s string) string { return paint(ansiBold+ansiGreen, s) }

// Bad marks a failure that did not stop the run.
func Bad(s string) string { return paint(ansiRed, s) }

// Quiet marks detail worth printing but not worth reading twice.
func Quiet(s string) string { return paint(ansiDim, s) }

// SetStyled turns styling on or off, for a test that needs a known answer.
func SetStyled(on bool) { styled = on }
