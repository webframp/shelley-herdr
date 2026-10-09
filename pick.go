package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type vm struct{ host, tags string }

// pick lists exe.dev VMs, optionally a recent conversation on the chosen one,
// then replaces this process with the bridge so the pane becomes the agent.
func pick() {
	in := bufio.NewReader(os.Stdin)
	ask := func(q string) string {
		fmt.Print(q)
		l, err := in.ReadString('\n')
		if err != nil && l == "" {
			os.Exit(0)
		}
		return strings.TrimSpace(l)
	}

	vms, err := listVMs()
	if err != nil || len(vms) == 0 {
		fmt.Fprintf(os.Stderr, "no exe.dev VMs found (%v)\n", err)
		ask("press enter to close ")
		os.Exit(1)
	}
	fmt.Println("\x1b[1mShelley on which VM?\x1b[0m")
	for i, v := range vms {
		fmt.Printf("%3d  %-28s \x1b[2m%s\x1b[0m\n", i+1, strings.TrimSuffix(v.host, ".exe.xyz"), v.tags)
	}
	target := choose(ask("number or name (blank to cancel): "), len(vms), func(i int) string { return vms[i].host },
		func(s string) bool { return false })
	var chosen *vm
	for i := range vms {
		switch {
		case target == vms[i].host, target+".exe.xyz" == vms[i].host:
			chosen = &vms[i]
		}
	}
	if chosen == nil {
		// fall back to a unique substring match
		for i := range vms {
			if strings.Contains(vms[i].host+" "+vms[i].tags, target) && target != "" {
				if chosen != nil {
					chosen = nil
					break
				}
				chosen = &vms[i]
			}
		}
	}
	if chosen == nil {
		os.Exit(0)
	}

	args := []string{"shelly-herdr"}
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
		a := ask("n for new, or number [n]: ")
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= len(convs) {
			args = append(args, "-c", convs[n-1].ID)
		}
	}
	args = append(args, chosen.host)
	exe, err := exec.LookPath("shelly-herdr")
	if err != nil {
		exe, _ = os.Executable()
	}
	fmt.Println()
	if err := syscall.Exec(exe, args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "exec:", err)
		os.Exit(1)
	}
}

func choose(a string, n int, name func(int) string, _ func(string) bool) string {
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
