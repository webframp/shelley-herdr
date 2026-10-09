// shelly-herdr runs a conversation with Shelley on a remote exe.dev VM inside
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
		fmt.Fprintln(os.Stderr, "usage: shelly-herdr [-c ID] [-cwd DIR] [-model M] <vm> [prompt...]")
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
	go func() { <-sig; s.release(); os.Exit(130) }()

	if s.convID != "" {
		// herdr only registers an agent on a non-idle first report.
		s.report("working", "loading history")
		s.render(false)
		s.report("idle", "resumed")
		s.refreshTitle()
	}
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	if p := strings.Join(flag.Args()[1:], " "); p != "" {
		s.turn(p)
	}
	for {
		fmt.Printf("\x1b[1m%s ›\x1b[0m ", s.label)
		line, ok := <-lines
		if !ok {
			s.release()
			return
		}
		if line = strings.TrimSpace(line); line != "" {
			s.turn(line)
		}
	}
}

// turn sends a prompt and streams the agent's response until the turn ends.
func (s *session) turn(prompt string) {
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
	for failures := 0; ; {
		err := s.render(true)
		if err == nil {
			if st, ok := s.status(); ok && st.Working {
				time.Sleep(time.Second)
				continue
			}
			break
		}
		if failures++; failures >= 3 {
			s.report("unknown", "connection to "+s.label+" lost; retrying")
		}
		time.Sleep(3 * time.Second)
	}
	s.report("idle", "")
	s.refreshTitle()
}

// render prints messages newer than lastSeq. With wait set it follows the
// live turn; otherwise it replays history. User text is echoed only for replay.
func (s *session) render(wait bool) error {
	args := []string{"read"}
	if wait {
		args = append(args, "-wait")
	}
	cmd := s.shelley(append(args, s.convID)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Seq <= s.lastSeq {
			continue
		}
		s.lastSeq = m.Seq
		switch {
		case m.Type == "user" && m.Text != "" && !wait:
			fmt.Printf("\n\x1b[1m› %s\x1b[0m\n", m.Text)
		case m.Type == "agent" && m.Text != "":
			fmt.Printf("\n%s\n", m.Text)
		case m.Type == "agent" && m.ToolName != "":
			fmt.Printf("\x1b[2m  ⚙ %s\x1b[0m\n", m.ToolName)
		}
	}
	return cmd.Wait()
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
	remote := "shelley client"
	for _, a := range args {
		remote += " " + shQuote(a)
	}
	return exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ControlMaster=auto",
		"-o", "ControlPath=~/.ssh/cm-shelly-%C", "-o", "ControlPersist=10m",
		"-o", "ServerAliveInterval=15", s.host, remote)
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
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	// Prefer the PATH entry for this binary: it survives upgrades, whereas the
	// resolved path (e.g. a Nix store path) goes stale.
	if p, err := exec.LookPath("shelly-herdr"); err == nil {
		if a, e1 := os.Stat(p); e1 == nil {
			if b, e2 := os.Stat(exe); e2 == nil && os.SameFile(a, b) {
				exe = p
			}
		}
	}
	// herdr also bans apostrophes, so pass arguments positionally, unquoted.
	return []string{"sh", "-c", `exec "$0" "$@"`, exe, "-c", s.convID, s.label}
}
