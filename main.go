// shelley-herdr runs a conversation with Shelley on a remote exe.dev VM inside
// a herdr pane and reports its state to herdr like a native agent.
//
// Transport is `ssh <vm> shelley client ...`; herdr is driven through the
// `herdr pane report-*` CLI, so there are no dependencies beyond the stdlib.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

const source = "custom:shelley"

type message struct {
	Seq       int    `json:"sequence_id"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	ToolName  string `json:"tool_name"`
	EndOfTurn bool   `json:"end_of_turn"`
}

type conversation struct {
	ID      string `json:"conversation_id"`
	Slug    string `json:"slug"`
	Working bool   `json:"working"`
}

type session struct {
	host, label, convID, cwd, model string
	lastSeq                         int
	herdr, pane                     string
	reported                        bool
	flags                           []string // client flags selecting the transport
	probed                          bool
	prompt                          string // the prompt of the turn in progress
	turnSeq                         int    // sequence of that prompt's user message, once seen
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "pick" {
		pick()
		return
	}
	convID := flag.String("c", "", "continue an existing Shelley conversation ID")
	cwd := flag.String("cwd", "", "working directory on the VM for a new conversation")
	model := flag.String("model", "", "Shelley model for a new conversation")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: shelley-herdr [-c ID] [-cwd DIR] [-model M] <vm> [prompt...]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	host := flag.Arg(0)
	label := strings.TrimSuffix(host, ".exe.xyz")
	if !strings.Contains(host, ".") {
		host += ".exe.xyz"
	}
	s := &session{host: host, label: label, convID: *convID, cwd: *cwd, model: *model}
	s.herdr = os.Getenv("HERDR_BIN_PATH")
	if s.herdr == "" {
		s.herdr, _ = exec.LookPath("herdr")
	}
	if os.Getenv("HERDR_ENV") == "1" {
		s.pane = os.Getenv("HERDR_PANE_ID")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; restoreTTY(); s.release(); os.Exit(130) }()

	if s.convID != "" {
		// herdr only registers an agent on a non-idle first report.
		s.report("working", "loading history")
		s.render(true)
		s.report("idle", "resumed")
		s.refreshTitle()
	}
	if p := strings.Join(flag.Args()[1:], " "); p != "" {
		s.turn(p)
	}
	rl := newLineReader(false)
	for {
		line, ok := rl.readLine(fmt.Sprintf("\x1b[1m%s ›\x1b[0m ", s.label), nil)
		if !ok {
			s.release()
			return
		}
		if line != "" {
			s.turn(line)
		}
	}
}

// turn sends a prompt and streams the agent's response until the turn ends.
func (s *session) turn(prompt string) {
	s.prompt, s.turnSeq = prompt, 0
	s.report("working", "sending prompt")
	args := []string{"chat", "-p", prompt}
	if s.convID != "" {
		args = append(args, "-c", s.convID)
	} else {
		if s.cwd != "" {
			args = append(args, "-cwd", s.cwd)
		}
		if s.model != "" {
			args = append(args, "-model", s.model)
		}
	}
	out, err := s.shelley(args...).Output()
	if err != nil {
		s.fail("chat", err)
		return
	}
	var c conversation
	if err := json.Unmarshal(out, &c); err != nil || c.ID == "" {
		s.fail("chat", fmt.Errorf("unexpected reply %q", out))
		return
	}
	first := s.convID == ""
	s.convID = c.ID
	if first {
		s.lastSeq = 0
		s.registerSession()
	}
	s.report("working", "")
	// Poll rather than `read -wait`: that streams only until the first
	// end-of-turn message in the history, so on a conversation that has finished
	// a turn it returns before the new reply.
	failures, quiet := 0, 0
	for ; ; time.Sleep(time.Second) {
		st, known := s.status() // before the read, so the read sees everything up to it
		ended, err := s.render(false)
		if err != nil {
			if failures++; failures == 3 {
				s.report("unknown", "connection to "+s.label+" lost; retrying")
			}
			time.Sleep(2 * time.Second)
			continue
		}
		if failures >= 3 {
			s.report("working", "")
		}
		failures = 0
		if ended {
			break
		}
		// The turn can also end without an end-of-turn message (an error).
		if known && !st.Working {
			if quiet++; quiet >= 3 {
				break
			}
		} else {
			quiet = 0
		}
	}
	s.report("idle", "")
	s.refreshTitle()
}

// render prints messages newer than lastSeq and reports whether one of them
// ended a turn. With replay set it also echoes the user's own text, which a live
// turn skips because the user just typed it.
func (s *session) render(replay bool) (ended bool, err error) {
	cmd := s.shelley("read", s.convID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return false, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Seq <= s.lastSeq {
			continue
		}
		s.lastSeq = m.Seq
		// Only an end after our own prompt ends our turn: the history can hold
		// other turns, including ones another client sent to this conversation.
		if m.Type == "user" && s.turnSeq == 0 && m.Text == s.prompt {
			s.turnSeq = m.Seq
		}
		ended = ended || (m.EndOfTurn && s.turnSeq != 0 && m.Seq > s.turnSeq)
		switch {
		case m.Type == "user" && m.Text != "" && replay:
			fmt.Printf("\n\x1b[1m› %s\x1b[0m\n", m.Text)
		case m.Type == "agent" && m.Text != "":
			fmt.Printf("\n%s\n", m.Text)
		case m.Type == "agent" && m.ToolName != "":
			fmt.Printf("\x1b[2m  ⚙ %s\x1b[0m\n", m.ToolName)
		}
	}
	return ended, cmd.Wait()
}

func (s *session) status() (conversation, bool) {
	out, err := s.shelley("list", "-limit", "50").Output()
	if err != nil {
		return conversation{}, false
	}
	for _, l := range strings.Split(string(out), "\n") {
		var c conversation
		if json.Unmarshal([]byte(l), &c) == nil && c.ID == s.convID {
			return c, true
		}
	}
	return conversation{}, false
}

func (s *session) shelley(args ...string) *exec.Cmd {
	s.detectTransport()
	return s.run(append(append([]string{}, s.flags...), args...)...)
}

func (s *session) run(args ...string) *exec.Cmd {
	remote := "shelley client"
	for _, a := range args {
		remote += " " + shQuote(a)
	}
	return exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ControlMaster=auto",
		"-o", "ControlPath=~/.ssh/cm-shelley-%C", "-o", "ControlPersist=10m",
		"-o", "ServerAliveInterval=15", s.host, remote)
}

// tcpFlags reach a Shelley that systemd socket-activates on localhost:9999 and
// that requires a user header, as exe.dev VMs do. Any value satisfies the header.
var tcpFlags = []string{"-url", "http://localhost:9999", "-H", "X-Exedev-Userid: shelley-herdr"}

// detectTransport picks how `shelley client` reaches the server on this VM,
// once per session. It uses the client's default (a unix socket) when that
// works, then the localhost TCP endpoint. A VM can have a stale unix socket
// file with nothing listening, so the default cannot be assumed.
func (s *session) detectTransport() {
	if s.probed {
		return
	}
	s.probed = true
	probe := func(flags []string) bool {
		return s.run(append(append([]string{}, flags...), "list", "-limit", "1")...).Run() == nil
	}
	if probe(nil) {
		return
	}
	if probe(tcpFlags) {
		s.flags = tcpFlags
	}
}

func shQuote(a string) string { return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'" }

func (s *session) fail(what string, err error) {
	var ee *exec.ExitError
	msg := err.Error()
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		msg = strings.TrimSpace(string(ee.Stderr))
	}
	fmt.Fprintf(os.Stderr, "shelley %s failed: %s\n", what, msg)
	s.report("idle", what+" failed")
}

// --- herdr reporting -------------------------------------------------------

func (s *session) herdrRun(args ...string) {
	if s.herdr == "" || s.pane == "" {
		return
	}
	out, err := exec.Command(s.herdr, append([]string{"pane"}, args...)...).CombinedOutput()
	if err == nil {
		return
	}
	// Never let a rejected resume command cost us the state update.
	if i := slices.Index(args, "--"); i >= 0 && strings.Contains(string(out), "resume_argv") {
		fmt.Fprintf(os.Stderr, "herdr: resume disabled: %s\n", strings.TrimSpace(string(out)))
		s.herdrRun(args[:i]...)
		return
	}
	fmt.Fprintf(os.Stderr, "herdr: %s\n", strings.TrimSpace(string(out)))
}

// report publishes the semantic state. Once the conversation ID is known the
// resume command rides along so herdr can restart the bridge after a restart.
func (s *session) report(state, msg string) {
	a := []string{"report-agent", s.pane, "--source", source, "--agent", "shelley@" + s.label,
		"--state", state, "--seq", fmt.Sprint(time.Now().UnixMilli())}
	if msg != "" {
		a = append(a, "--message", msg)
	}
	if s.convID != "" {
		a = append(a, "--agent-session-id", s.convID, "--")
		a = append(a, s.resumeArgv()...)
	}
	s.herdrRun(a...)
}

func (s *session) registerSession() {
	s.herdrRun("report-metadata", s.pane, "--source", source, "--agent", "shelley@"+s.label,
		"--display-agent", "shelley@"+s.label)
}

func (s *session) refreshTitle() {
	if c, ok := s.status(); ok && c.Slug != "" {
		s.herdrRun("report-metadata", s.pane, "--source", source, "--agent", "shelley@"+s.label,
			"--title", c.Slug)
	}
}

func (s *session) release() {
	s.herdrRun("release-agent", s.pane, "--source", source, "--agent", "shelley@"+s.label)
}

func (s *session) resumeArgv() []string {
	// herdr rejects a path as argv[0] and its server PATH may not include our
	// install dir, so hand it a plain `sh` that execs the absolute path.
	// herdr also bans apostrophes, so pass arguments positionally, unquoted.
	return []string{"sh", "-c", `exec "$0" "$@"`, binPath(), "-c", s.convID, s.label}
}

// binPath is the path to run this program again. It prefers the PATH entry when
// that is the same file as the running binary: the PATH entry survives upgrades,
// whereas the resolved path (e.g. a Nix store path) goes stale.
func binPath() string {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	if p, err := exec.LookPath("shelley-herdr"); err == nil {
		if a, e1 := os.Stat(p); e1 == nil {
			if b, e2 := os.Stat(exe); e2 == nil && os.SameFile(a, b) {
				exe = p
			}
		}
	}
	return exe
}
