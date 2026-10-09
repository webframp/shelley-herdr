#!/usr/bin/env bash
# Behavioural suite, run inside a container after setup-$FLAVOR.sh has installed
# the plugin. The same assertions run for every install method, so a difference
# between methods shows up as a failure in exactly one of them.
set -u
: "${FLAVOR:?FLAVOR must be github or nix}"
. /repo/test/lib.sh

export FAKE_STATE=/tmp/fake-shelley FAKE_TURN_MS=2500
rm -rf "$FAKE_STATE"

echo "TAP version 13"
echo "# flavor: $FLAVOR"

start_server || exit 1
herdr workspace create >/dev/null

root=$(plugin_root)

# --- registration and binary resolution --------------------------------------
check "plugin is registered" bash -c 'herdr plugin list | grep -q webframp.shelly'
case "$FLAVOR" in
github)
  check "registered from GitHub source" bash -c 'herdr plugin list | grep -q "github:webframp/shelly-herdr@"'
  check "install built the binary into the plugin directory" test -x "$root/shelly-herdr"
  check "binary is not on PATH (fallback path is what runs)" bash -c '! command -v shelly-herdr'
  ;;
nix)
  check "registered from a Nix store path" bash -c 'herdr plugin list | grep -q "local:/nix/store/"'
  check "binary is on PATH from the Home Manager profile" bash -c 'command -v shelly-herdr | grep -q "^$HOME/.nix-profile/bin/"'
  ;;
esac

# --- picker ------------------------------------------------------------------
p=$(open_picker) || { nok "picker pane opened"; summary; exit 1; }
if wait_for "$p" 'Shelley on which VM' 10; then ok "picker pane opened"; else nok "picker pane opened"; show_screen "$p"; summary; exit 1; fi
check "picker lists running VMs"        bash -c "$(declare -f screen); screen $p | grep -q alpha-one"
check "picker hides stopped VMs"        bash -c "$(declare -f screen); ! screen $p | grep -q gamma-three"

# --- new conversation: state transitions, title, output ----------------------
say "$p" alpha-one
if wait_for "$p" 'alpha-one ›' 10; then ok "bridge started after choosing a VM"; else nok "bridge started after choosing a VM"; show_screen "$p"; summary; exit 1; fi

: >/tmp/states
watch_status "$p" /tmp/states & watcher=$!
say "$p" "write a haiku"
if wait_for "$p" 'ack: write a haiku' 20; then ok "reply is rendered"; else nok "reply is rendered"; show_screen "$p"; fi
sleep 1
kill "$watcher" 2>/dev/null; wait "$watcher" 2>/dev/null
states=$(uniq /tmp/states | tr '\n' ' ')
echo "# observed states: $states"
check "agent was reported as working"    bash -c 'grep -qx working /tmp/states'
check "agent finished (done or idle)"    bash -c 'tail -n1 /tmp/states | grep -qE "^(done|idle)$"'
check "tool call is shown"               bash -c "$(declare -f screen); screen $p | grep -q '⚙ bash'"
check "agent name is shelley@<vm>"       test "$(agent_field "$p" agent)" = "shelley@alpha-one"
for i in 1 2 3 4 5 6 7 8; do [ "$(herdr agent get "$p" | grep -o '"title":"[^"]*"' | cut -d'"' -f4)" = "write-a-haiku" ] && break; sleep 0.5; done
check "pane title is the conversation slug" test "$(herdr agent get "$p" | grep -o '"title":"[^"]*"' | cut -d'"' -f4)" = "write-a-haiku"

# --- resume command saved ----------------------------------------------------
for i in $(seq 1 10); do [ -n "$(resume_argv)" ] && break; sleep 0.5; done
argv=$(resume_argv)
echo "# saved resume: $argv"
check "resume command is saved with the conversation id" bash -c "echo '$argv' | grep -q '\"-c\",\"cFAKE001\",\"alpha-one\"'"
check "resume command starts with plain sh"              bash -c "echo '$argv' | grep -q '\"argv\":\[\"sh\",\"-c\"'"
case "$FLAVOR" in
github) check "resume command uses the installed plugin binary" bash -c "echo '$argv' | grep -qE '/plugins/github/webframp\.shelly-[0-9a-f]+/shelly-herdr'" ;;
nix)    check "resume command uses the stable profile path, not a store path" bash -c "echo '$argv' | grep -q '$HOME/.nix-profile/bin/shelly-herdr' && ! echo '$argv' | grep -q '/nix/store/'" ;;
esac

# --- resume through the picker ----------------------------------------------
p2=$(open_picker)
wait_for "$p2" 'Shelley on which VM' 10
say "$p2" alpha-one
if wait_for "$p2" 'Conversations on alpha-one' 10; then ok "picker offers existing conversations"; else nok "picker offers existing conversations"; show_screen "$p2"; fi
check "conversation slug is listed" bash -c "$(declare -f screen); screen $p2 | grep -q write-a-haiku"
say "$p2" 1
if wait_for "$p2" 'ack: write a haiku' 10; then ok "history replays on resume"; else nok "history replays on resume"; show_screen "$p2"; fi
sleep 1
check "resumed pane is registered as an agent" test "$(agent_field "$p2" agent)" = "shelley@alpha-one"

# --- dropped connection mid-turn ---------------------------------------------
echo 14000 >"$FAKE_STATE/turn_ms"; echo 3 >"$FAKE_STATE/fail_reads"
: >/tmp/states2
watch_status "$p" /tmp/states2 & watcher=$!
say "$p" "slow one"
if wait_for "$p" 'ack: slow one' 45; then ok "bridge recovers after a dropped connection"; else nok "bridge recovers after a dropped connection"; show_screen "$p"; fi
sleep 1
kill "$watcher" 2>/dev/null; wait "$watcher" 2>/dev/null
echo "# observed states: $(uniq /tmp/states2 | tr '\n' ' ')"
check "state became unknown while the link was down" bash -c 'grep -qx unknown /tmp/states2'
check "state ended done or idle after recovery"      bash -c 'tail -n1 /tmp/states2 | grep -qE "^(done|idle)$"'
rm -f "$FAKE_STATE/turn_ms"

# --- restart: herdr relaunches the bridge with the saved command -------------
stop_server
start_server || exit 1
relaunched=
for i in $(seq 1 60); do
  proc_running '(^|/)shelly-herdr -c cFAKE001 alpha-one' && { relaunched=1; break; }
  sleep 0.5
done
if [ -n "$relaunched" ]; then ok "bridge is relaunched after a server restart"; else nok "bridge is relaunched after a server restart"; fi
restored=
for q in $(panes); do
  if [ "$(agent_field "$q" agent)" = "shelley@alpha-one" ]; then restored=$q; break; fi
done
if [ -n "$restored" ]; then ok "restored pane is registered as shelley@alpha-one"; else nok "restored pane is registered as shelley@alpha-one"; fi
[ -n "$restored" ] && check "restored pane shows the conversation history" bash -c "$(declare -f screen); screen $restored | grep -q 'ack: write a haiku'"

# --- re-running setup is idempotent ------------------------------------------
/repo/test/setup-$FLAVOR.sh reinstall >/tmp/reinstall.log 2>&1 || { echo "# reinstall output:"; sed 's/^/#   /' /tmp/reinstall.log | tail -20; }
check "plugin is still registered exactly once after re-running setup" test "$(herdr plugin list | grep -c '^- webframp.shelly ')" = 1
if [ "$FLAVOR" = github ]; then
  newroot=$(plugin_root)
  check "binary still exists after reinstall" test -x "$newroot/shelly-herdr"
fi

# --- launcher: PATH wins, plugin-directory binary is the fallback (github) ---
if [ "$FLAVOR" = github ]; then
  cmd=$(sed -n "s/^command = \[\"sh\", \"-c\", '''\(.*\)'''\]\$/\1/p" "$(plugin_root)/herdr-plugin.toml")
  stub=$(mktemp -d); printf '#!/bin/sh\necho PATH-BINARY-WON\n' >"$stub/shelly-herdr"; chmod +x "$stub/shelly-herdr"
  check "launcher command was extracted from the manifest" test -n "$cmd"
  check "launcher prefers shelly-herdr on PATH" bash -c "cd '$(plugin_root)' && PATH='$stub':\$PATH sh -c '$cmd' </dev/null 2>&1 | grep -q PATH-BINARY-WON"
  check "launcher falls back to the built binary" bash -c "cd '$(plugin_root)' && sh -c '$cmd' </dev/null 2>&1 | grep -q 'Shelley on which VM'"
fi

echo "1..$((pass + failn))"
summary
