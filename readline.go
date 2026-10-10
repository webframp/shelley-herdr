package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// lineReader reads one line at a time with the usual editing keys, history and
// optional Tab completion. It puts the terminal in raw mode through stty, which
// keeps the program stdlib-only. When stdin is not a terminal (or stty fails) it
// falls back to plain line reads.
type lineReader struct {
	in         *bufio.Reader
	history    []string
	escCancels bool // a lone Esc cancels the line (the picker); otherwise it is ignored
}

func newLineReader(escCancels bool) *lineReader {
	return &lineReader{in: bufio.NewReader(os.Stdin), escCancels: escCancels}
}

// readLine prints prompt and returns the line. complete returns the candidates
// for the text typed so far; it may be nil. ok is false on cancel (Ctrl-C,
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
	pos := 0
	hist, draft := len(r.history), ""
	redraw := func() {
		fmt.Printf("\r\x1b[K%s%s", prompt, string(buf))
		if back := len(buf) - pos; back > 0 {
			fmt.Printf("\x1b[%dD", back)
		}
	}
	set := func(s string) { buf, pos = []rune(s), len([]rune(s)); redraw() }
	del := func(from, to int) { buf = append(buf[:from], buf[to:]...); pos = from; redraw() }
	wordLeft := func() int {
		i := pos
		for i > 0 && buf[i-1] == ' ' {
			i--
		}
		for i > 0 && buf[i-1] != ' ' {
			i--
		}
		return i
	}
	wordRight := func() int {
		i := pos
		for i < len(buf) && buf[i] == ' ' {
			i++
		}
		for i < len(buf) && buf[i] != ' ' {
			i++
		}
		return i
	}
	move := func(to int) { pos = to; redraw() }
	prev := func() {
		if hist > 0 {
			if hist == len(r.history) {
				draft = string(buf)
			}
			hist--
			set(r.history[hist])
		}
	}
	next := func() {
		if hist < len(r.history) {
			hist++
			if hist == len(r.history) {
				set(draft)
			} else {
				set(r.history[hist])
			}
		}
	}

	for {
		c, _, err := r.in.ReadRune()
		if err != nil {
			return "", false
		}
		switch {
		case c == '\r' || c == '\n':
			fmt.Print("\r\n")
			line = strings.TrimSpace(string(buf))
			if line != "" && (len(r.history) == 0 || r.history[len(r.history)-1] != line) {
				r.history = append(r.history, line)
			}
			return line, true
		case c == 3 || (c == 4 && len(buf) == 0): // Ctrl-C, Ctrl-D on empty
			fmt.Print("\r\n")
			return "", false
		case c == 4 && pos < len(buf): // Ctrl-D deletes forward
			del(pos, pos+1)
		case c == 1: // Ctrl-A
			move(0)
		case c == 5: // Ctrl-E
			move(len(buf))
		case c == 2 && pos > 0: // Ctrl-B
			move(pos - 1)
		case c == 6 && pos < len(buf): // Ctrl-F
			move(pos + 1)
		case c == 11: // Ctrl-K
			buf = buf[:pos]
			redraw()
		case c == 21: // Ctrl-U
			del(0, pos)
		case c == 23: // Ctrl-W
			del(wordLeft(), pos)
		case c == 12: // Ctrl-L
			fmt.Print("\x1b[2J\x1b[H")
			redraw()
		case c == 16: // Ctrl-P
			prev()
		case c == 14: // Ctrl-N
			next()
		case c == 0x7f || c == 8:
			if pos > 0 {
				del(pos-1, pos)
			}
		case c == 0x1b:
			if r.in.Buffered() == 0 {
				if r.escCancels {
					fmt.Print("\r\n")
					return "", false
				}
				continue
			}
			seq := r.escapeSeq()
			switch seq {
			case "[A", "OA":
				prev()
			case "[B", "OB":
				next()
			case "[C", "OC":
				if pos < len(buf) {
					move(pos + 1)
				}
			case "[D", "OD":
				if pos > 0 {
					move(pos - 1)
				}
			case "[H", "OH", "[1~", "[7~":
				move(0)
			case "[F", "OF", "[4~", "[8~":
				move(len(buf))
			case "[3~":
				if pos < len(buf) {
					del(pos, pos+1)
				}
			case "[1;5C", "[1;3C", "f": // Ctrl/Alt-Right, Alt-f
				move(wordRight())
			case "[1;5D", "[1;3D", "b": // Ctrl/Alt-Left, Alt-b
				move(wordLeft())
			case "\x7f": // Alt-Backspace
				del(wordLeft(), pos)
			}
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
			pos = len(buf)
			redraw()
		case c >= ' ':
			buf = append(buf, 0)
			copy(buf[pos+1:], buf[pos:])
			buf[pos] = c
			pos++
			if pos == len(buf) {
				fmt.Print(string(c))
			} else {
				redraw()
			}
		}
	}
}

// escapeSeq reads the rest of an escape sequence that is already buffered: a
// CSI or SS3 sequence up to its final byte, or a single Alt-modified key.
func (r *lineReader) escapeSeq() string {
	c, _, _ := r.in.ReadRune()
	if c != '[' && c != 'O' {
		return string(c)
	}
	seq := string(c)
	for r.in.Buffered() > 0 {
		n, _, _ := r.in.ReadRune()
		seq += string(n)
		if n >= '@' && n <= '~' {
			break
		}
	}
	return seq
}

// ttyRestore undoes rawMode. Signal handlers call it so a killed bridge never
// leaves the terminal without echo.
var ttyRestore func()

func restoreTTY() {
	if ttyRestore != nil {
		ttyRestore()
	}
}

// rawMode switches the terminal to unbuffered, no-echo input with signals off
// (Ctrl-C arrives as a byte) and returns a function that restores it. Output
// processing stays on, so a plain \n still returns the carriage.
func rawMode() (restore func(), err error) {
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	saved, err := get.Output()
	if err != nil {
		return nil, err
	}
	set := exec.Command("stty", "-icanon", "-echo", "-isig", "min", "1")
	set.Stdin = os.Stdin
	if err := set.Run(); err != nil {
		return nil, err
	}
	restore = func() {
		undo := exec.Command("stty", strings.TrimSpace(string(saved)))
		undo.Stdin = os.Stdin
		_ = undo.Run()
		ttyRestore = nil
	}
	ttyRestore = restore
	return restore, nil
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
