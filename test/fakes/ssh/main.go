// Command ssh is a fake ssh for tests. `ssh exe.dev ls` prints a canned VM list
// in exe.dev's format; any other host runs the remote command locally, where a
// fake `shelley` is on PATH.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const vmList = `Your VMs:
  • alpha-one.exe.xyz - running (boldsoftware/exeuntu) #demo
  • beta-two.exe.xyz - running (boldsoftware/exeuntu)
  • gamma-three.exe.xyz - stopped (boldsoftware/exeuntu) #retired
`

func main() {
	args := os.Args[1:]
	// Skip options: -o takes a value; the rest here are bare flags.
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] == "-o" || args[0] == "-F" || args[0] == "-i" || args[0] == "-p" {
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fake ssh: no host")
		os.Exit(255)
	}
	host, cmd := args[0], strings.Join(args[1:], " ")
	if host == "exe.dev" {
		if strings.TrimSpace(cmd) == "ls" {
			fmt.Print(vmList)
			return
		}
		fmt.Fprintf(os.Stderr, "fake ssh: unsupported exe.dev command %q\n", cmd)
		os.Exit(1)
	}
	c := exec.Command("sh", "-c", cmd)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(255)
	}
}
