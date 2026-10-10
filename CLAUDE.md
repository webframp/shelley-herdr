# shelly-herdr

Runs [Shelley](https://exe.dev) conversations on remote exe.dev VMs as native
[herdr](https://herdr.dev) agents. Shelley is a web agent with no terminal UI, so
a local bridge process drives `shelley client` over SSH, prints the conversation
in a herdr pane, and reports state to herdr. The conversation itself keeps
running on the VM.

Go, standard library only. Do not add dependencies.

## Layout

| Path | Purpose |
| --- | --- |
| `main.go` | The bridge: one pane, one conversation. Sends prompts, streams replies, reports state. |
| `pick.go` | `shelly-herdr pick`: lists VMs from `ssh exe.dev ls`, then recent conversations, then creates a herdr workspace and runs the bridge in its shell pane (outside herdr it `exec`s the bridge). `readline.go` adds Tab completion through `stty`. |
| `herdr-plugin.toml` | herdr plugin manifest: one action (`open`), one pane (`picker`), one build step. |
| `flake.nix` | Package (`packages.default`, includes the plugin dir), `homeManagerModules.default`, overlay, dev shell. |
| `test/` | Container tests for both install methods. See Testing. |
| `Makefile` | `build`, `clean`, `clean-all`, `test`. |

## How it works

- Transport: `ssh <vm>.exe.xyz shelley client {chat,read,list}`. `read -wait`
  replays history, then streams. The bridge dedupes by `sequence_id`.
- State goes to herdr by shelling out to `herdr pane report-agent`,
  `report-metadata` and `release-agent` (no socket code). Source is
  `custom:shelley`; agent label is `shelley@<vm>`.
- States: `working` while a turn runs, `idle` when it ends, `unknown` after three
  failed stream reads. Shelley never asks for tool approval, so the bridge never
  reports `blocked`. herdr may display a finished turn as `done` until viewed.
- The pane title is the conversation slug, set after each turn.
- The resume command rides along with each report once the conversation ID is
  known, so herdr relaunches the bridge after a restart.
- `shelley client` is marked experimental upstream, and exposes tool names but not
  tool arguments or output. Tool calls render as a dim `⚙ name` line.

## herdr constraints that shaped the code

These were learned by testing herdr 0.9.3. Check them before changing the related
code, because herdr fails silently or rejects with terse errors.

- **Resume command:** `resume_argv` must start with a plain command name (not a
  path) and must not contain apostrophes. The code sends
  `sh -c 'exec "$0" "$@"' <abs-path> -c <id> <vm>`. `herdrRun` retries without
  the resume command if herdr rejects it, so a bad resume command never costs the
  state update.
- **First report:** a first report of `idle` does not register an agent. Resuming
  reports `working` while it replays history, then `idle`.
- **Binary path in the resume command:** prefer the `PATH` entry when it is the
  same file as the running binary (`os.SameFile`). A Nix store path goes stale
  after an upgrade; `~/.nix-profile/bin/shelly-herdr` does not.
- **Plugin panes are not agent terminals.** The picker is a `popup` plugin pane. A popup
  is not a pane: it has no pane ID and no pane or agent APIs, so a headless test
  can only confirm the action succeeded, and `test/lib.sh` runs the picker command
  in a plain pane instead. The 0.9.3 CLI does not list `popup` or `--width/--height`
  for `plugin pane open`, but the 0.9.3 server accepts it from the manifest. The bridge runs in a plain workspace root pane started with
  `herdr workspace create` and `herdr pane run`, so herdr treats it as a local
  terminal.
- **Plugin commands:** they run with the plugin directory as the working
  directory. The server's `PATH` is what it started with, and often lacks
  `~/.local/bin`. The picker pane therefore runs `shelly-herdr` from `PATH` and
  falls back to `./shelly-herdr` (the binary the install built).
- **Plugin install vs link:** `herdr plugin install owner/repo` clones with git,
  runs `[[build]]` after confirmation, and registers a managed checkout.
  `herdr plugin link <dir>` never builds. Both work with no server running.
  herdr refuses to install over a linked plugin.
- **Keybindings** are not declarable in the manifest. Users bind
  `type = "plugin_action"`, `command = "webframp.shelly.open"` in `config.toml`.
- **Plugin registry is global and shared:** `~/.config/herdr/plugins.json`.
  Overriding `HOME`, `XDG_CONFIG_HOME`, `HERDR_CONFIG_PATH` or `HERDR_SOCKET_PATH`
  does not isolate it. Never run `herdr plugin link/install/uninstall` against the
  developer's own herdr while experimenting: it rewrites their live plugin
  registry. Use the containers.

## Install paths

Two methods that end in the same registered plugin (`webframp.shelly`):

1. **Home Manager:** `programs.shelly-herdr.enable = true`. The module installs the
   package and runs `herdr plugin link <store path>` on every switch (after
   `installPackages`). Failure or a missing `herdr` warns but never fails the switch.
2. **Everything else:** `herdr plugin install webframp/shelly-herdr`, which builds
   the binary into the plugin directory.

The module does not manage a keybinding.

## Testing

```sh
make test                  # go vet, then both install methods
make test FLAVOR=github    # herdr plugin install, no Nix
make test FLAVOR=nix       # Home Manager activation
```

Needs podman or docker and network. Each method runs in a container against a
snapshot of the working tree (uncommitted changes included), driving a headless
herdr server with a fake `ssh` and fake `shelley` (`test/fakes`). No test touches a
live VM. The same `test/suite.sh` runs for both methods.

Gotchas when editing the tests:

- The `nixos/nix` image is minimal: no `/bin/bash` (use `#!/usr/bin/env bash`), no
  `pgrep` (use `proc_running` in `test/lib.sh`).
- Both containers run as `HOME=/home/tester`. The nix flavour needs a clean user
  profile, or Home Manager conflicts with the image's own packages.
- Fake `shelley` mirrors the real client's output as observed, not as specified.
  `test/fakes/shelley` reads `turn_ms` and `fail_reads` files in `$FAKE_STATE` so a
  test can change turn length or simulate a dropped connection mid-run.
- The suite proves little unless it can fail. When changing it, mutate the product
  (for example, make the resume command an absolute path) and confirm a test
  fails, then restore the file.
- `make clean-all` also deletes the cached Nix store volume. The next nix run
  rebuilds Home Manager and is slow.

Not covered: the real Shelley, keybindings (they need an attached client), and
platforms other than Linux x86_64. Home Manager and `nixos/nix:latest` are not
pinned, so upstream changes can break the nix run on their own.

## Shipping boundary

Test code must never reach a user's machine, and the packaging must prove it.

The general problem: build and packaging tools select by pattern, not by intent.
`buildGoModule` builds every `main` package, `go build ./...` and `go install
./...` do the same, and a `cp -r` or glob in an install step takes whatever matches.
Anything that shares the pattern with product code ships with it. A test double
is the worst case, because it is built to impersonate something real. A fake named
`ssh` installed into a profile's `bin/` shadows the real `ssh` for every program
the user runs, not just this one. The test suite cannot catch this by itself: it
runs with the fakes first on `PATH`, so a leaked fake looks like a working install.
v0.1.0 shipped fake `ssh` and `shelley` binaries this way.

Rules:

- **Name the product explicitly.** Build only what ships: `subPackages = ["."]` in
  `flake.nix`, `go build -o shelly-herdr .` in the manifest. Never rely on a
  default or a wildcard to select the product, and never use `./...` for anything
  that installs.
- **Never give a fake the name of a real tool on a user's `PATH`.** Keep fakes out
  of any package that gets installed, and build them only inside the test
  containers (`test/run.sh`).
- **Check what the artifact contains, not what the source contains.** For each
  install method, list the installed files and assert that they are exactly the
  expected set (see the "installs only shelly-herdr" check in `test/suite.sh`). A
  new file in the artifact should fail a test until someone decides it belongs.
- **Prove the check can fail.** Revert the fix, confirm the new check fails, then
  restore it.
- **Before a release, look at the built output.** `nix build` and `ls result/bin`,
  or the plugin directory after `herdr plugin install`, and read the file list.

## Conventions

- Match the existing comment density: short comments that explain why, not what.
- Run `make test` before changing install or resume behavior, and again after.
- Write README and doc prose in plain active voice, without filler or hedging.
- The repo is public. A version tag and the `herdr-plugin` GitHub topic (which
  lists the plugin in herdr's marketplace) are release decisions for the
  maintainer; a routine change should not add either.
