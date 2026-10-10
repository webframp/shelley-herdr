# shelley-herdr

Run [Shelley](https://exe.dev) conversations on remote exe.dev VMs as native
[herdr](https://herdr.dev) agents. Each conversation gets a herdr pane with a
live state (`working`, `idle`, `unknown`), a title taken from the conversation
slug, and a resume command, so herdr can bring it back after a restart. The
agent itself keeps running on the VM.

Shelley is a web agent, not a terminal program. `shelley-herdr` bridges the two:
it drives `shelley client` over SSH, prints the conversation in the pane, and
reports state to herdr through `herdr pane report-agent`. It depends only on the
Go standard library.

## Requirements

- herdr 0.9 or newer, running locally.
- An exe.dev account, with your SSH key registered. These must work without a
  password prompt:
  ```sh
  ssh exe.dev ls
  ssh <vm>.exe.xyz shelley client list
  ```
- VMs with Shelley installed (the default `exeuntu` image has it) and a
  Shelley recent enough to include `shelley client`.

## Install

Both methods end in the same place: herdr has the `webframp.shelley` plugin
registered, and `shelley-herdr` is available to run. Choose by how you manage
your machine.

| | Home Manager | Everything else |
| --- | --- | --- |
| You run | add a flake input, then switch | `herdr plugin install webframp/shelley-herdr` |
| Needs | Nix with flakes, Home Manager | `git` and Go 1.22+ |
| Who registers the plugin | the module, on every switch | `herdr plugin install` |
| Who provides the binary | the Nix package, on your `PATH` | the install's build step |
| To update | update the flake input and switch | run the install command again |

### Home Manager

Add the flake as an input and enable the module:

```nix
{
  inputs.shelley-herdr.url = "github:webframp/shelley-herdr";
  inputs.shelley-herdr.inputs.nixpkgs.follows = "nixpkgs";
}
```

```nix
{
  imports = [inputs.shelley-herdr.homeManagerModules.default];
  programs.shelley-herdr.enable = true;
}
```

After a switch, the module has installed the package and linked the plugin. If
`herdr` isn't installed yet, the switch prints the one command to run later
instead of failing. The module doesn't manage a keybinding; add one under
[Keybinding](#keybinding).

### Everything else

```sh
herdr plugin install webframp/shelley-herdr
```

herdr shows a preview, including the build command (`go build`), and asks for
confirmation; pass `--yes` to skip the prompt. It clones the repository, builds
the binary into the plugin directory, and registers the plugin. To update, run
the same command again.

### Check it worked

```sh
herdr plugin list     # shows webframp.shelley
```

Then continue with [Keybinding](#keybinding) and [Use](#use). Nothing after
this point depends on which method you chose.

Other ways to install (a plain Nix package, running without installing, local
development) are under [Development](#development).

## Use

From any herdr workspace, invoke the plugin action, or [bind it to a key](#keybinding):

```sh
herdr plugin action invoke open --plugin webframp.shelley
```

A popup opens with your running VMs, discovered from `ssh exe.dev ls`.
Pick one by number or name (Tab completes names), then pick a recent
conversation or start a new one. The popup closes and a workspace named
`shelley@<vm>` opens, with the conversation running in an ordinary terminal pane.
herdr lists it in the sidebar like any local agent.

Or skip the picker and run the bridge directly in any herdr pane:

```sh
shelley-herdr <vm> "fix the failing tests"        # new conversation
shelley-herdr -c <conversation-id> <vm>           # resume
shelley-herdr -cwd /srv/app -model <model> <vm>   # new, with options
```

`<vm>` is a VM name (`my-vm`) or full hostname (`my-vm.exe.xyz`). Type a prompt
at the `›` prompt; press Ctrl-D or Ctrl-C to leave. The conversation keeps
running on the VM.

Outside herdr the bridge still works as a plain chat client; it only skips the
state reporting.

## Keybinding

Plugin manifests don't declare keys. Bind the action in herdr's `config.toml`
(this example uses `prefix+a`; pick any key you haven't bound):

```toml
[[keys.command]]
key = "prefix+a"
type = "plugin_action"
command = "webframp.shelley.open"
description = "Shelley on an exe.dev VM"
```

Run `herdr config check` afterwards, then reload with your reload-config key or
`herdr server reload-config`. If you manage `config.toml` through Home Manager,
add the block to the source file and switch.

## How it works

| herdr sees | Source |
| --- | --- |
| `working` | A prompt was sent, or the VM reports the turn still running |
| `idle` | The turn finished. herdr may display this as `done` (finished, not yet viewed) |
| `unknown` | Three failed attempts to read the stream (SSH dropped) |
| Pane title | The conversation slug |
| Resume command | `sh -c 'exec "$0" "$@"' <shelley-herdr> -c <id> <vm>` |

Shelley never pauses for tool approval, so the bridge never reports `blocked`.

## Limitations

- The bridge finds how to reach Shelley on each VM: the client's default unix
  socket first, then `localhost:9999` with the header exe.dev VMs require. A VM
  that exposes Shelley another way is not supported.
- `shelley client` is marked experimental, and its output may change.
- It exposes tool names but not their arguments or output, so tool calls
  appear as one dim line (`⚙ bash`).
- A prompt typed during a running turn waits until the turn ends.
- While you are idle at the prompt, the bridge checks the conversation every two
  seconds and prints new replies above your line, so a late reply or a turn added
  from another client appears without a keypress.
- Line editing covers cursor keys, Home/End, Ctrl-A/E/B/F/K/U/W, Alt-b/f and
  history (Up/Down) within one session. History is not saved between sessions, and
  a line longer than the pane width does not redraw cleanly.
- Resume after a herdr restart works only if herdr can run the saved command.
  herdr rejects paths and apostrophes in it, which is why it goes through `sh`.

## Testing

```sh
test/run.sh           # both install methods
test/run.sh github    # herdr plugin install, no Nix
test/run.sh nix       # Home Manager module
```

Needs `podman` or `docker` and network access; the first run downloads images
and, for `nix`, builds Home Manager (cached in a volume afterwards;
`test/run.sh clean` removes it). A run takes about a minute once cached.

Each method runs in its own container against a snapshot of your working tree,
so uncommitted changes are what gets tested. The same suite (`test/suite.sh`)
runs for both, so a difference between methods fails in exactly one of them. It
covers registration, binary resolution, the picker, state transitions
(`working`, `unknown` after a dropped connection, `idle`), the pane title, the
saved resume command, resuming through the picker, a herdr server restart
relaunching the bridge, and re-running setup. `ssh` and `shelley` are replaced by
fakes in `test/fakes`, so no VM is ever contacted.

What the tests do not cover: the real Shelley (the fake reproduces the
`shelley client` output format as observed, not as specified), keybindings
(they need an attached terminal client), and platforms other than Linux x86_64.

## Development

```sh
nix develop     # Go toolchain, or use your own
go vet ./... && go build ./...
nix build       # the package, including the plugin directory
```

### Run from a checkout

```sh
git clone https://github.com/webframp/shelley-herdr
cd shelley-herdr
go build -o shelley-herdr .
herdr plugin link "$PWD"
```

`plugin link` never runs build commands, so build first. herdr refuses to
install over a linked plugin, so run `herdr plugin unlink webframp.shelley`
before you try the GitHub install.

### Nix without Home Manager

Add the package yourself, then register the plugin once. The package installs it
under `share/herdr/plugins/shelley`:

```nix
home.packages = [inputs.shelley-herdr.packages.${pkgs.stdenv.hostPlatform.system}.default];
```

```sh
herdr plugin link "$(dirname "$(dirname "$(readlink -f "$(command -v shelley-herdr)")")")/share/herdr/plugins/shelley"
```

The link points into the Nix store, so run it again after you upgrade the
package. An overlay is also available (`overlays.default`, providing
`pkgs.shelley-herdr`).

### Try without installing

```sh
nix run github:webframp/shelley-herdr -- <vm>
```

### Put the binary on your PATH after a GitHub install

The plugin picker works without this. To also run `shelley-herdr` directly from
any pane (see [Use](#use)):

```sh
go install github.com/webframp/shelley-herdr@latest   # installs to $(go env GOBIN), default ~/go/bin
```

## License

Apache-2.0. See [LICENSE](LICENSE).
