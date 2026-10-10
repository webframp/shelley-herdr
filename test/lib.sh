# Helpers shared by the container scenarios. Sourced, not executed.

pass=0
failn=0

ok()  { echo "ok - $1"; pass=$((pass + 1)); }
nok() { echo "not ok - $1"; [ -n "${2:-}" ] && printf '#   %s\n' "$2"; failn=$((failn + 1)); }

# check DESC CMD...: pass if CMD succeeds.
check() { local d=$1; shift; if "$@" >/dev/null 2>&1; then ok "$d"; else nok "$d"; fi; }

# show_screen PANE: dump a pane to the test log for diagnosis.
show_screen() { echo "# --- screen of $1:"; screen "$1" | sed 's/^/#   /'; }

start_server() {
  herdr server >/tmp/server.log 2>&1 &
  local i
  for i in $(seq 1 40); do
    herdr status 2>&1 | grep -q 'status: running' && return 0
    sleep 0.25
  done
  echo "# herdr server did not start"; cat /tmp/server.log; return 1
}

stop_server() { herdr server stop >/dev/null 2>&1; sleep 2; }

panes() { herdr pane list | grep -o '"pane_id":"[^"]*"' | cut -d'"' -f4 | sort; }

# popup_action: invoke the plugin action. The picker is a popup, which is not a
# pane and has no pane APIs, so a headless server can only confirm the action
# succeeded; the picker's own behavior is tested in a plain pane (open_picker).
popup_action() {
  local i
  herdr plugin action invoke open --plugin webframp.shelly >/dev/null 2>&1 || return 1
  for i in $(seq 1 20); do
    herdr plugin log 2>&1 | grep -q '"action_id":"open".*"status":"succeeded"' && return 0
    sleep 0.25
  done
  return 1
}

# open_picker: run the manifest's picker command in a new plain pane and print
# the pane id. HERDR_ENV and the pane id are set by herdr, as in a popup.
open_picker() {
  local before new i cmd q
  cmd=$(grep '^command = \["sh"' "$(plugin_root)/herdr-plugin.toml")
  q="'''"
  cmd=${cmd#*"$q"}; cmd=${cmd%"$q"*}
  before=$(panes)
  herdr pane split --direction right >/dev/null 2>&1
  for i in $(seq 1 40); do
    new=$(comm -13 <(echo "$before") <(panes) | head -1)
    [ -n "$new" ] && break
    sleep 0.25
  done
  [ -n "$new" ] || return 1
  herdr pane run "$new" "cd '$(plugin_root)' && sh -c '$cmd'" >/dev/null 2>&1
  echo "$new"
}

# new_pane BEFORE: print the id of a pane that was not in the BEFORE list, once
# one appears and is not the (closing) picker.
new_pane() {
  local i new
  for i in $(seq 1 40); do
    new=$(comm -13 <(echo "$1" | sort) <(panes) | while read -r q; do
      screen "$q" | grep -qE 'Shelley on which VM|pane_not_found' || echo "$q"
    done | head -1)
    [ -n "$new" ] && { echo "$new"; return 0; }
    sleep 0.25
  done
  return 1
}

say()    { herdr pane send-text "$1" "$2" >/dev/null; herdr pane send-keys "$1" enter >/dev/null; }
screen() { herdr pane read "$1" --source visible --lines 120 2>&1; }

# wait_for PANE REGEX [SECONDS]
wait_for() {
  local p=$1 re=$2 t=${3:-15} i
  for i in $(seq 1 $((t * 4))); do
    screen "$p" | grep -qE "$re" && return 0
    sleep 0.25
  done
  return 1
}

agent_field() { herdr agent get "$1" 2>/dev/null | grep -o "\"$2\":\"[^\"]*\"" | head -1 | cut -d'"' -f4; }

# watch_status PANE FILE: sample the agent status until killed.
watch_status() {
  while :; do agent_field "$1" agent_status >>"$2"; sleep 0.15; done
}

plugin_root() {
  local r
  r=$(grep -o '"plugin_root": *"[^"]*"' ~/.config/herdr/plugins.json | head -1 | cut -d'"' -f4)
  # A linked plugin (Home Manager) has no managed checkout; use the link target.
  if [ -z "$r" ]; then
    r=$(herdr plugin list | grep -o '\[local:[^]]*\]' | head -1)
    r=${r#\[local:}; r=${r%\]}
  fi
  echo "$r"
}

# resume_argv: the saved resume command of any shelley agent, as one line.
resume_argv() {
  tr -d ' \n' <~/.config/herdr/session.json 2>/dev/null | grep -o '"agent_resume":{[^}]*}' | grep shelley | head -1
}

# proc_running REGEX: true if any process command line matches (no pgrep needed;
# the nixos/nix image is minimal). Anchor REGEX so this shell never matches itself.
proc_running() {
  local f
  for f in /proc/[0-9]*/cmdline; do
    tr '\0' ' ' <"$f" 2>/dev/null | grep -qE "$1" && return 0
  done
  return 1
}

summary() {
  echo "# $pass passed, $failn failed"
  [ "$failn" -eq 0 ]
}
