package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// lineReader reads one line at a time with Tab completion. It puts the
// terminal in raw mode through stty, which keeps the program stdlib-only. When
// stdin is not a terminal (or stty fails) it falls back to plain line reads.
type lineReader struct {
	in *bufio.Reader
}

func newLineReader() *lineReader { return &lineReader{in: bufio.NewReader(os.Stdin)} }

// readLine prints prompt and returns the line. complete returns the candidates
// for the text typed so far; it may be nil. ok is false on cancel (Esc, Ctrl-C,
// Ctrl-D on an empty line) and on end of input.
func (r *lineReader) readLine(prompt string, complete func(string) []string) (line string, ok bool) {
	fmt.Print(prompt)
	restore, err := rawMode()
	if err != nil {
		l, err := r.in.ReadString('\n')
		if err != nil && l == "" {
			return "", false
		}
		return strings.TrimSpace(l), true
	}
	defer restore()

	var buf []rune
	redraw := func() { fmt.Printf("\r\x1b[K%s%s", prompt, string(buf)) }
	for {
		c, _, err := r.in.ReadRune()
		if err != nil {
			return "", false
		}
		switch {
		case c == '\r' || c == '\n':
			fmt.Print("\r\n")
			return strings.TrimSpace(string(buf)), true
		case c == 0x1b && r.in.Buffered() > 0:
			// An escape sequence (arrow keys and the like): swallow it.
			r.in.ReadRune()
			for r.in.Buffered() > 0 {
				n, _, _ := r.in.ReadRune()
				if n >= '@' && n <= '~' {
					break
				}
			}
		case c == 3 || c == 0x1b || (c == 4 && len(buf) == 0):
			fmt.Print("\r\n")
			return "", false
		case c == 0x7f || c == 8:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				redraw()
			}
		case c == 21: // Ctrl-U
			buf = buf[:0]
			redraw()
		case c == '\t' && complete != nil:
			cands := complete(string(buf))
			if len(cands) == 0 {
				continue
			}
			if p := commonPrefix(cands); len(p) > len(buf) {
				buf = []rune(p)
				if len(cands) == 1 {
					buf = append(buf, ' ')
				}
			} else if len(cands) > 1 {
				fmt.Printf("\r\n%s\r\n", strings.Join(cands, "  "))
			}
			redraw()
		case c >= ' ' && c != 0x7f:
			buf = append(buf, c)
			fmt.Print(string(c))
		}
	}
}

// rawMode switches the terminal to raw, no-echo input and returns a function
// that restores it. Output post-processing stays on elsewhere, so prints in
// raw mode use explicit \r\n.
func rawMode() (restore func(), err error) {
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	saved, err := get.Output()
	if err != nil {
		return nil, err
	}
	set := exec.Command("stty", "raw", "-echo")
	set.Stdin = os.Stdin
	if err := set.Run(); err != nil {
		return nil, err
	}
	return func() {
		undo := exec.Command("stty", strings.TrimSpace(string(saved)))
		undo.Stdin = os.Stdin
		_ = undo.Run()
	}, nil
}

// completeNames returns the names that start with the typed prefix, sorted.
func completeNames(names []string) func(string) []string {
	return func(prefix string) []string {
		var out []string
		for _, n := range names {
			if strings.HasPrefix(n, prefix) {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return out
	}
}

func commonPrefix(ss []string) string {
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}
