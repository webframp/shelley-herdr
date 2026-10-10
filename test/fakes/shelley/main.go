// Command shelley is a fake `shelley client` for tests. It speaks the same
// JSONL protocol as the real client, keeps state in $FAKE_STATE, and simulates a
// turn that takes $FAKE_TURN_MS (default 2500) so state changes are observable.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type msg struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	End  bool   `json:"end"`
	AtMS int64  `json:"at_ms"`
}

type conv struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Created   int64  `json:"created_ms"`
	Updated   int64  `json:"updated_ms"`
	TurnEndMS int64  `json:"turn_end_ms"`
	Msgs      []msg  `json:"msgs"`
}

func stateDir() string {
	d := os.Getenv("FAKE_STATE")
	if d == "" {
		d = "/tmp/fake-shelley"
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

func now() int64 { return time.Now().UnixMilli() }

func turnMS() int64 {
	// A turn_ms file in the state dir overrides the env, so a test can change
	// the duration after the server (and its panes) have started.
	if b, err := os.ReadFile(filepath.Join(stateDir(), "turn_ms")); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			return v
		}
	}
	if v, err := strconv.ParseInt(os.Getenv("FAKE_TURN_MS"), 10, 64); err == nil {
		return v
	}
	return 2500
}

func load(id string) (*conv, error) {
	b, err := os.ReadFile(filepath.Join(stateDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var c conv
	return &c, json.Unmarshal(b, &c)
}

func (c *conv) save() {
	b, _ := json.Marshal(c)
	_ = os.WriteFile(filepath.Join(stateDir(), c.ID+".json"), b, 0o644)
}

func (c *conv) working() bool { return now() < c.TurnEndMS }

func (c *conv) add(typ, text, tool string, end bool, at int64) {
	c.Msgs = append(c.Msgs, msg{Seq: len(c.Msgs) + 1, Type: typ, Text: text, Tool: tool, End: end, AtMS: at})
}

func slugify(s string) string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,:;!?'\"")
		if w != "" {
			out = append(out, w)
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		return "conversation"
	}
	return strings.Join(out, "-")
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "fake shelley: "+format+"\n", a...)
	os.Exit(1)
}

// opts parses the flags the real client accepts: value flags, bool flags, and
// one positional argument.
func opts(args []string, valued ...string) (map[string]string, []string) {
	isVal := map[string]bool{}
	for _, v := range valued {
		isVal[v] = true
	}
	m := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		k := strings.TrimLeft(a, "-")
		if isVal[k] && i+1 < len(args) {
			m[k] = args[i+1]
			i++
		} else {
			m[k] = "true"
		}
	}
	return m, pos
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "client" || len(args) < 2 {
		fail("usage: shelley client <chat|read|list> ...")
	}
	// Global flags precede the subcommand, as in the real client.
	args = args[1:]
	url, hdr := "", ""
	for len(args) >= 2 && (args[0] == "-url" || args[0] == "-H") {
		if args[0] == "-url" {
			url = args[1]
		} else {
			hdr = args[1]
		}
		args = args[2:]
	}
	if len(args) == 0 {
		fail("usage: shelley client <chat|read|list> ...")
	}
	// A VM whose unix socket is stale: only the TCP endpoint, with its header, works.
	if _, err := os.Stat(filepath.Join(stateDir(), "unix_down")); err == nil {
		switch {
		case url == "":
			fail("Error: Post \"http://localhost/api/conversations/new\": dial unix /home/exedev/.config/shelley/shelley.sock: connect: connection refused")
		case !strings.Contains(hdr, "X-Exedev-Userid:"):
			fail("Error: HTTP 403")
		}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "chat":
		chat(rest)
	case "read":
		read(rest)
	case "list":
		list(rest)
	case "archive", "tag", "tags", "search":
		fmt.Println("{}")
	default:
		fail("unknown subcommand %q", sub)
	}
}

func chat(args []string) {
	o, _ := opts(args, "p", "c", "cwd", "model")
	prompt := o["p"]
	if prompt == "" {
		fail("-p is required")
	}
	t := now()
	var c *conv
	if id := o["c"]; id != "" {
		var err error
		if c, err = load(id); err != nil {
			fail("no conversation %s", id)
		}
		if c.working() {
			fail("conversation %s is mid-turn", id)
		}
	} else {
		files, _ := filepath.Glob(filepath.Join(stateDir(), "c*.json"))
		c = &conv{ID: fmt.Sprintf("cFAKE%03d", len(files)+1), Slug: slugify(prompt), Created: t}
		c.add("system", "You are Shelley.", "", false, t)
	}
	T := turnMS()
	c.add("user", prompt, "", false, t)
	c.add("agent", "", "bash", false, t+T/5)
	c.add("user", "", "", false, t+T*3/10)
	c.add("agent", "ack: "+prompt, "", true, t+T)
	c.TurnEndMS, c.Updated = t+T, t
	c.save()
	fmt.Printf("{\"conversation_id\":%q}\n", c.ID)
}

func read(args []string) {
	o, pos := opts(args)
	if len(pos) != 1 {
		fail("usage: read [-wait] ID")
	}
	// Like the real client, -wait ends at the first end-of-turn message in the
	// history, which is not necessarily the live turn.
	wait := o["wait"] == "true"
	// Simulate an SSH drop: consume one unit from the fail_reads counter.
	p := filepath.Join(stateDir(), "fail_reads")
	if b, err := os.ReadFile(p); err == nil {
		if n, _ := strconv.Atoi(strings.TrimSpace(string(b))); n > 0 {
			_ = os.WriteFile(p, []byte(strconv.Itoa(n-1)), 0o644)
			fmt.Fprintln(os.Stderr, "fake shelley: connection dropped")
			os.Exit(255)
		}
	}
	printed := 0
	for {
		c, err := load(pos[0])
		if err != nil {
			fail("no conversation %s", pos[0])
		}
		for printed < len(c.Msgs) && c.Msgs[printed].AtMS <= now() {
			m := c.Msgs[printed]
			out := map[string]any{"sequence_id": m.Seq, "type": m.Type, "end_of_turn": m.End}
			if m.Text != "" {
				out["text"] = m.Text
			}
			if m.Tool != "" {
				out["tool_name"] = m.Tool
			}
			b, _ := json.Marshal(out)
			fmt.Println(string(b))
			printed++
			if wait && m.End {
				return
			}
		}
		if !wait || (printed == len(c.Msgs) && !c.working()) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func list(args []string) {
	o, _ := opts(args, "limit", "q")
	limit := 50
	if n, err := strconv.Atoi(o["limit"]); err == nil {
		limit = n
	}
	files, _ := filepath.Glob(filepath.Join(stateDir(), "c*.json"))
	var cs []*conv
	for _, f := range files {
		if c, err := load(strings.TrimSuffix(filepath.Base(f), ".json")); err == nil {
			cs = append(cs, c)
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Updated > cs[j].Updated })
	for i, c := range cs {
		if i >= limit {
			break
		}
		fmt.Printf("{\"conversation_id\":%q,\"slug\":%q,\"created_at\":%q,\"updated_at\":%q,\"working\":%t,\"model\":\"fake\"}\n",
			c.ID, c.Slug, time.UnixMilli(c.Created).UTC().Format(time.RFC3339),
			time.UnixMilli(c.Updated).UTC().Format(time.RFC3339), c.working())
	}
}
