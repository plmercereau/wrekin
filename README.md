# Wrekin

> To all agents round the Wrekin.

Wrekin moves a Claude Code Cloud session onto a machine you own, so that the
session continues in a local terminal, on your files, with your tools. You ask
for it from Claude itself (Mobile, Web or Desktop); nothing has to be open,
exposed or reachable on the target machine.

It is made of two parts. A small Go daemon runs on each of your machines as
your user and keeps one outbound WebSocket to a relay. The relay is a
Cloudflare Worker that authenticates you, exposes a remote MCP server to
Claude and forwards your teleport requests to the right machine. The relay
never holds a secret that could open a session on your machine; the machine
decides what it will do, within limits you configure.

Status: MVP, Claude Code only. See [Status](#status) before relying on it.

## How it works

1. In Claude, you ask to teleport the current Claude Code Cloud session to one
   of your machines ("teleport this to my workstation").
2. Claude calls the `list_nodes` MCP tool if it has to pick a destination.
3. Claude passes the Cloud session identifier to the `teleport` tool.
4. The relay checks who you are and forwards the command to the connected
   daemon. If the machine is offline, the request fails at once; nothing is
   queued.
5. The daemon checks the request against its local policy: allowed
   directories, concurrency limit, available tools.
6. It finds the repository among your checkouts or proposes a path to clone
   it into, always under a directory you allowed.
7. If the repository is new to this machine, you confirm the clone and the
   trust Claude Code should grant it, through Claude.
8. The daemon starts `claude --teleport <session>` inside a tmux session, in
   an isolated git worktree.
9. The job becomes `ready` once the Claude Code prompt is visible.
10. Whenever you sit down at the machine, `wrekin attach <session>` puts you
    in that terminal.

## Install

The daemon is one static binary for Linux and macOS, amd64 and arm64. It
needs `git`, `tmux` and [Claude Code](https://docs.anthropic.com/claude-code)
installed on the machine.

Homebrew (macOS and Linux):

```sh
brew install plmercereau/tap/wrekin
```

Nix:

```sh
nix run github:plmercereau/wrekin -- --version
# or, in a flake: inputs.wrekin.url = "github:plmercereau/wrekin";
#   packages: wrekin.packages.${system}.wrekin
#   Home Manager: imports = [ wrekin.homeManagerModules.wrekin ]; services.wrekin.enable = true;
```

The Nix flake covers `x86_64-linux`, `aarch64-linux` and `aarch64-darwin`
(nixpkgs no longer supports Intel Macs; use Homebrew or the release archive
there).

GitHub Releases: download the archive for your platform from
<https://github.com/plmercereau/wrekin/releases>, verify it against
`checksums.txt`, and put `wrekin` on your `PATH`.

## Quick start

1. Pair the machine with your account. This generates an Ed25519 key pair on
   the machine and shows a code and a URL; open the URL on any device, sign
   in with GitHub or Google, and approve the machine:

   ```sh
   wrekin auth --name workstation
   ```

2. Start the daemon as a user-level service (systemd `--user` on Linux,
   launchd on macOS). With Homebrew you can use `brew services start wrekin`
   instead; with Home Manager, `services.wrekin.enable = true`.

   ```sh
   wrekin start
   wrekin status
   ```

3. Connect Claude to Wrekin: in Claude's connector settings, add the remote
   MCP server `https://wrekin.platyplus.io/mcp` and complete the sign-in.

4. From a Claude Code Cloud session, ask Claude to teleport it to the machine
   by name. Later, on the machine:

   ```sh
   wrekin sessions
   wrekin attach <session>
   ```

## Command line

| Command | What it does |
|---|---|
| `wrekin auth` | Pair this machine: generate the key pair, show the pairing code, register the node |
| `wrekin start` | Install and start the user-level service (`--foreground` runs the daemon in the terminal) |
| `wrekin stop` | Stop the service |
| `wrekin status` | Relay connection, node identity, job counts (`--json` available) |
| `wrekin sessions` | List the teleport jobs and their tmux sessions |
| `wrekin attach <session>` | Attach to a session's terminal (`tmux attach`) |
| `wrekin logs` | Show the daemon log (`-f` to follow, `-n` for the line count) |
| `wrekin cleanup` | Remove the tmux sessions, worktrees and clones Wrekin created (`--dry-run`, `--yes`); never a repository that existed before Wrekin |
| `wrekin logout` | Unregister the node from your account and delete the local identity |

## Configuration

`~/.config/wrekin/settings.json` (`XDG_CONFIG_HOME` is honoured). `wrekin auth`
writes it with the defaults if it does not exist.

```json
{
  "name": "workstation",
  "relay": {
    "url": "wss://wrekin.platyplus.io/connect"
  },
  "workspace": {
    "allowedDirectories": ["~/projects", "~/work"],
    "defaultDirectory": "~/projects",
    "autoDiscover": true
  },
  "teleport": {
    "terminal": "tmux",
    "maxConcurrent": 4
  },
  "logging": {
    "level": "info"
  }
}
```

- `workspace.allowedDirectories`: the only roots a teleport may use. Paths
  are resolved canonically; a symbolic link that leaves a root is refused.
- `workspace.defaultDirectory`: where new clones are proposed.
- `workspace.autoDiscover`: look for an existing checkout of the repository
  under the allowed roots before proposing a clone.
- `teleport.maxConcurrent`: simultaneous teleport preparations (sessions
  already running are not counted).

Other files, all local to the machine: the node identity (private key) in
`~/.config/wrekin/identity.json` (mode 0600), jobs in
`~/.local/state/wrekin/jobs.json`, the log in `~/.local/state/wrekin/wrekin.log`
and the control socket `~/.local/state/wrekin/wrekin.sock`
(`XDG_STATE_HOME` is honoured).

## Security model

- You sign in to the relay with GitHub or Google. Claude is authorised to call
  the MCP server through OAuth; you approve that once, from Claude.
- Each machine has its own Ed25519 key pair, generated locally by
  `wrekin auth` and approved from a signed-in browser. The daemon proves its
  identity by signing a challenge on every connection. The private key never
  leaves the machine.
- The relay stores, per node: an id, your account, a name, the public key, an
  agent version and timestamps. It never receives private keys, Git or Claude
  Code credentials, terminal output or local logs. It does receive what it
  needs to route: authentication metadata and the MCP arguments of a teleport,
  notably the Cloud session identifier.
- The daemon enforces the limits itself: allowed directories, canonical paths,
  concurrency, no interactive Git prompt (Git runs with prompts disabled and a
  timeout), no deletion of repositories it did not create. There is no global
  bypass of Claude Code permissions.
- Accepted trade-off: a teleport runs automatically, without a second
  authorisation factor per command. A compromised relay could therefore
  trigger the operations the protocol allows, within the limits the daemon
  enforces locally. The confirmation asked for a new repository prevents
  accidental approvals; it is not a defence against a compromised relay.

The contract between daemon and relay is a versioned protocol, independent of
Claude Code: [`protocol/PROTOCOL.md`](protocol/PROTOCOL.md).

## Status

This is the MVP. It is being built against four feasibility questions that
decide whether the approach holds: P1, can Claude obtain the Cloud session
identifier a teleport needs; P2, does `claude --teleport` work inside a
detached tmux session and how do we know the session is ready; P3, does git
worktree isolation combine with teleport, and can the repository trust be
prepared safely; P4, does the authentication stack (Better Auth on D1 and
Cloudflare Workers, the MCP authorisation server, Ed25519 pairing through
device authorisation) work end to end.

Their state, what was verified and what still has to be checked on Claude
Mobile/Web and on the deployed Worker, is tracked in
[`docs/feasibility.md`](docs/feasibility.md).

## Repository layout

```
cmd/wrekin/         the binary's entry point
internal/           the Go agent: CLI, config, identity, relay client, workspace policy,
                    git, tmux, Claude Code adapter, jobs, daemon, service, pairing
cloud/              the Cloudflare Worker: Hono, Better Auth, Durable Object, MCP tools
protocol/           PROTOCOL.md, the node protocol (normative for both halves)
nix/                Nix package and Home Manager module (flake.nix at the root)
packaging/          how releases, Homebrew and the Worker deploy are produced
docs/               design spec, implementation plan, feasibility notes
.github/workflows/  ci.yml, release.yml (the Worker is deployed by Workers Builds, not by Actions)
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) to run both halves locally.

## Licence

[MIT](LICENSE), Pierre-Louis Mercereau, 2026.
