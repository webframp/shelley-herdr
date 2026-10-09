# shelly-herdr

Run [Shelley](https://exe.dev) conversations on remote exe.dev VMs as native
[herdr](https://herdr.dev) agents. Each conversation gets a herdr pane with a
live state (`working`, `idle`, `unknown`), a title taken from the conversation
slug, and a resume command, so herdr can bring it back after a restart. The
agent itself keeps running on the VM.

Shelley is a web agent, not a terminal program. `shelly-herdr` bridges the two:
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

## Install with Nix

Add the flake as an input, the same way you would any other:

```nix
{
  inputs.shelly-herdr.url = "github:webframp/shelly-herdr";
  # inputs.shelly-herdr.inputs.nixpkgs.follows = "nixpkgs";
}
```

### Home Manager (recommended)

The module installs the package and registers the herdr plugin on every
switch, so the registration never goes stale after an upgrade:

```nix
{
  imports = [inputs.shelly-herdr.homeManagerModules.default];
  programs.shelly-herdr.enable = true;
}
```

After a switch, `herdr plugin list` shows `webframp.shelly`. If `herdr` isn't
installed yet, the switch prints the one command to run later instead of
failing. The module doesn't manage a keybinding; see [Keybinding](#keybinding).

### Package only

Without Home Manager, add the package yourself:

```nix
home.packages = [inputs.shelly-herdr.packages.${pkgs.stdenv.hostPlatform.system}.default];
```

An overlay is also available (`overlays.default`, providing `pkgs.shelly-herdr`).
Then register the plugin once. The package installs it under
`share/herdr/plugins/shelly`:

```sh
herdr plugin link "$(dirname "$(dirname "$(readlink -f "$(command -v shelly-herdr)")")")/share/herdr/plugins/shelly"
```

The link points into the Nix store, so run it again after you upgrade the
package.

To try the bridge without installing:

```sh
nix run github:webframp/shelly-herdr -- <vm>
```

## Install without Nix

You need `git` and Go 1.22 or newer. One command clones the repository, builds
the binary into the plugin directory, and registers the plugin:

```sh
herdr plugin install webframp/shelly-herdr
```

herdr shows a preview, including the build command (`go build`), and asks for
confirmation. Pass `--yes` to skip the prompt. To update, run the same command
again; herdr replaces its managed checkout.

The plugin picker finds `shelly-herdr` on `PATH` first, then falls back to the
binary the install built, so the picker works with no further setup. To also run
the bridge directly from any pane (see [Use](#use)), put it on your `PATH`:

```sh
go install github.com/webframp/shelly-herdr@latest   # installs to $(go env GOBIN), default ~/go/bin
```

### Developing locally

```sh
git clone https://github.com/webframp/shelly-herdr
cd shelly-herdr
go build -o shelly-herdr .
herdr plugin link "$PWD"
```

`plugin link` never runs build commands, so build first. herdr refuses to
install over a linked plugin, so run `herdr plugin unlink webframp.shelly`
before you try the GitHub install.

## Use

From any herdr workspace, invoke the plugin action, or [bind it to a key](#keybinding):

```sh
herdr plugin action invoke open --plugin webframp.shelly
```

A split pane opens with your running VMs, discovered from `ssh exe.dev ls`.
Pick one by number or name, then pick a recent conversation or start a new one.
The pane becomes the agent.

Or skip the picker and run the bridge directly in any herdr pane:

```sh
shelly-herdr <vm> "fix the failing tests"        # new conversation
shelly-herdr -c <conversation-id> <vm>           # resume
shelly-herdr -cwd /srv/app -model <model> <vm>   # new, with options
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
command = "webframp.shelly.open"
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
| Resume command | `sh -c 'exec "$0" "$@"' <shelly-herdr> -c <id> <vm>` |

Shelley never pauses for tool approval, so the bridge never reports `blocked`.

## Limitations

- `shelley client` is marked experimental, and its output may change.
- It exposes tool names but not their arguments or output, so tool calls
  appear as one dim line (`⚙ bash`).
- A prompt typed during a running turn waits until the turn ends.
- Resume after a herdr restart works only if herdr can run the saved command.
  herdr rejects paths and apostrophes in it, which is why it goes through `sh`.

## Development

```sh
nix develop     # Go toolchain, or use your own
go vet ./... && go build ./...
nix build       # the package, including the plugin directory
```

## License

Apache-2.0. See [LICENSE](LICENSE).
