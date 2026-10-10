package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type vm struct{ host, tags string }

func (v vm) name() string { return strings.TrimSuffix(v.host, ".exe.xyz") }

// pick lists exe.dev VMs, optionally a recent conversation on the chosen one,
// then starts the bridge. Inside herdr the bridge gets its own workspace, so it
// looks like a local terminal running the agent; elsewhere this process becomes
// the bridge.
func pick() {
	rl := newLineReader()
	ask := func(q string, complete func(string) []string) string {
		l, ok := rl.readLine(q, complete)
		if !ok {
			os.Exit(0)
		}
		return l
	}

	vms, err := listVMs()
	if err != nil || len(vms) == 0 {
		fmt.Fprintln(os.Stderr, "no exe.dev VMs found", err)
		ask("press enter to close ", nil)
		os.Exit(1)
	}
	fmt.Println("\x1b[1mShelley on which VM?\x1b[0m")
	names := make([]string, len(vms))
	for i, v := range vms {
		names[i] = v.name()
		fmt.Printf("%3d  %-28s \x1b[2m%s\x1b[0m\n", i+1, v.name(), v.tags)
	}
	target := choose(ask("number or name (Tab completes, blank to cancel): ", completeNames(names)), len(vms),
		func(i int) string { return vms[i].host })
	var chosen *vm
	for i := range vms {
		if target == vms[i].host || target+".exe.xyz" == vms[i].host {
			chosen = &vms[i]
		}
	}
	if chosen == nil && target != "" {
		// fall back to a unique substring match
		for i := range vms {
			if strings.Contains(vms[i].host+" "+vms[i].tags, target) {
				if chosen != nil {
					fmt.Fprintf(os.Stderr, "%q matches more than one VM\n", target)
					ask("press enter to close ", nil)
					os.Exit(1)
				}
				chosen = &vms[i]
			}
		}
	}
	if chosen == nil {
		if target != "" {
			fmt.Fprintf(os.Stderr, "no VM matches %q\n", target)
			ask("press enter to close ", nil)
			os.Exit(1)
		}
		os.Exit(0)
	}

	var args []string
	convs := recent(chosen.host)
	if len(convs) > 0 {
		fmt.Printf("\n\x1b[1mConversations on %s\x1b[0m\n  n  new conversation\n", chosen.host)
		for i, c := range convs {
			w := ""
			if c.Working {
				w = " (working)"
			}
			fmt.Printf("%3d  %s%s\n", i+1, c.Slug, w)
		}
		a := ask("n for new, or number [n]: ", nil)
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= len(convs) {
			args = append(args, "-c", convs[n-1].ID)
		}
	}
	args = append(args, chosen.host)
	fmt.Println()

	if inHerdr() {
		if err := openWorkspace(chosen.name(), args); err == nil {
			return
		} else {
			fmt.Fprintln(os.Stderr, "herdr workspace:", err)
		}
	}
	exe := binPath()
	if err := syscall.Exec(exe, append([]string{"shelly-herdr"}, args...), os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "exec:", err)
		os.Exit(1)
	}
}

func inHerdr() bool {
	return os.Getenv("HERDR_ENV") == "1" && os.Getenv("HERDR_BIN_PATH") != ""
}

// openWorkspace creates a herdr workspace and types the bridge command into its
// shell, so the conversation lives in an ordinary terminal pane that herdr
// shows in the sidebar like any local agent.
func openWorkspace(label string, args []string) error {
	herdr := os.Getenv("HERDR_BIN_PATH")
	create := []string{"workspace", "create", "--label", "shelley@" + label, "--focus"}
	if cwd := focusedCwd(); cwd != "" {
		create = append(create, "--cwd", cwd)
	}
	out, err := exec.Command(herdr, create...).Output()
	if err != nil {
		return err
	}
	var r struct {
		Result struct {
			RootPane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.Result.RootPane.ID == "" {
		return fmt.Errorf("unexpected workspace reply %q", out)
	}
	cmd := shQuote(binPath())
	for _, a := range args {
		cmd += " " + shQuote(a)
	}
	if out, err := exec.Command(herdr, "pane", "run", r.Result.RootPane.ID, cmd).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// focusedCwd is the working directory of the pane that invoked the plugin, so
// the new workspace starts where the user was rather than in the plugin directory.
func focusedCwd() string {
	var c struct {
		Cwd string `json:"focused_pane_cwd"`
	}
	_ = json.Unmarshal([]byte(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")), &c)
	return c.Cwd
}

func choose(a string, n int, name func(int) string) string {
	if i, err := strconv.Atoi(a); err == nil && i >= 1 && i <= n {
		return name(i - 1)
	}
	return a
}

// listVMs observes the VM fleet via `ssh exe.dev ls` rather than a declared list.
func listVMs() ([]vm, error) {
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", "exe.dev", "ls").Output()
	if err != nil {
		return nil, err
	}
	var vms []vm
	for _, l := range strings.Split(string(out), "\n") {
		_, rest, ok := strings.Cut(l, "•")
		if !ok || !strings.Contains(rest, " - running") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v := vm{host: f[0]}
		for _, w := range f[1:] {
			if strings.HasPrefix(w, "#") {
				v.tags += w + " "
			}
		}
		vms = append(vms, v)
	}
	return vms, nil
}

func recent(host string) []conversation {
	s := &session{host: host}
	out, err := s.shelley("list", "-limit", "8").Output()
	if err != nil {
		return nil
	}
	var cs []conversation
	for _, l := range strings.Split(string(out), "\n") {
		var c conversation
		if json.Unmarshal([]byte(l), &c) == nil && c.ID != "" {
			cs = append(cs, c)
		}
	}
	return cs
}
