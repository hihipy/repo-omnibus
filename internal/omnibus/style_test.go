package omnibus

import (
	"strings"
	"testing"
)

func TestStylingWrapsAndResets(t *testing.T) {
	SetStyled(true)
	t.Cleanup(func() { SetStyled(false) })

	got := Notice("look here")
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("a styled string must reset at the end, got %q", got)
	}
	if !strings.Contains(got, "look here") {
		t.Errorf("the text should survive, got %q", got)
	}
}

func TestStylingResetsBeforeEveryNewline(t *testing.T) {
	// A colour left open across a newline bleeds into the rest of the screen.
	SetStyled(true)
	t.Cleanup(func() { SetStyled(false) })

	got := Good("first\nsecond")
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("line %q does not reset", line)
		}
	}
}

func TestStylingOffLeavesTextAlone(t *testing.T) {
	SetStyled(false)
	for _, f := range []func(string) string{Bold, Notice, Good, Bad, Quiet} {
		if got := f("plain"); got != "plain" {
			t.Errorf("with styling off, got %q", got)
		}
	}
	if Notice("") != "" {
		t.Error("an empty string should stay empty")
	}
}
