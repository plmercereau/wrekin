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

The daemon is one static binary for Linux and macOS, amd64 and arm64. At
runtime it runs three programs it does not ship, looked up on its PATH:
`git`, `tmux` and [Claude Code](https://docs.anthropic.com/claude-code)
(`claude`). Install Claude Code with its native installer
(`curl -fsSL https://claude.ai/install.sh | bash`, which keeps
`~/.local/bin/claude` up to date by itself), with
`brew install --cask claude-code` on macOS, or from nixpkgs (`claude-code`,
an unfree package). Each channel below brings the rest.

Homebrew (macOS and Linux):

```sh
brew install plmercereau/tap/wrekin
```

The formula depends on tmux; git is already there, Homebrew requires it.
`brew services start wrekin` runs the daemon with `~/.local/bin`, Homebrew's
prefix and the system directories on its PATH, so it finds `claude` from
either installer.

Nix:

```sh
nix run github:plmercereau/wrekin -- --version
```

The package wraps the binary so that `git` and `tmux` are found even on a
machine that has neither; they are appended to the PATH, so your own come
first. Claude Code is not bundled (see above). In a flake:

```nix
inputs.wrekin.url = "github:plmercereau/wrekin";
# The package: wrekin.packages.${system}.wrekin
# The daemon as a user service, with Home Manager:
{
  imports = [ wrekin.homeManagerModules.wrekin ];
  services.wrekin = {
    enable = true;
    settings.name = "workstation";
    settings.workspace.allowedDirectories = [ "~/projects" ];
    # claudePackage = pkgs.claude-code; # the default when nixpkgs.config.allowUnfree is set, null otherwise
    # path = [ pkgs.git pkgs.tmux ];    # what the daemon finds after ~/.local/bin and your profile
  };
}
```

The module runs `wrekin daemon` under `systemd --user` on Linux and launchd
on macOS, with a PATH of `~/.local/bin`, your Home Manager profile, Homebrew
on macOS, `services.wrekin.path` (git, tmux, and `claudePackage` when set)
and the system directories, in that order: what you installed yourself wins.
Pairing (`wrekin auth`) stays a manual, one-time step; the `wrekin` the
module installs has the same tools appended to its PATH, so `wrekin auth`
signs in the Claude Code the daemon will launch. The flake covers
`x86_64-linux`, `aarch64-linux` and `aarch64-darwin` (nixpkgs no longer
supports Intel Macs; use Homebrew or the release archive there).

GitHub Releases: download the archive for your platform from
<https://github.com/plmercereau/wrekin/releases>, verify it against
`checksums.txt`, and put `wrekin` on your `PATH`, along with `git`, `tmux`
and `claude`.

## Quick start

1. Pair the machine with your account. This generates an Ed25519 key pair on
   the machine and shows a code and a URL; open the URL on any device, sign
   in with GitHub or Google, and approve the machine:

   ```sh
   wrekin auth --name workstation
   ```

   Then it checks that Claude Code is signed in on the machine, since every
   teleport runs `claude --teleport` with your credentials, and runs
   `claude auth login` when it is not (on a terminal; `--no-claude-login`
   only reports). On a machine already paired, `wrekin auth` runs that check
   alone. A teleport to a node whose Claude Code is signed out fails at once
   with `harness_unavailable`, before anything is cloned.

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
| `wrekin auth` | Pair this machine: generate the key pair, show the pairing code, register the node; then sign Claude Code in if it is not |
| `wrekin start` | Install and start the user-level service (`--foreground` runs the daemon in the terminal) |
| `wrekin stop` | Stop the service |
| `wrekin status` | Relay connection, node identity, job counts (`--json` available) |
| `wrekin sessions` | List the teleport jobs and their tmux sessions |
| `wrekin attach <session>` | Attach to a session's terminal: `tmux -S ~/.local/state/wrekin/tmux.sock attach`, Wrekin's own tmux server, named by its socket so the daemon and your shell reach the same one whatever `TMUX_TMPDIR` says |
| `wrekin logs` | Show the daemon log (`-f` to follow, `-n` for the line count) |
| `wrekin kill <session\|job>...`, `wrekin kill --all` | End teleported sessions: kill their tmux session and end their job (`session_gone`, so teleporting again needs relaunch); `--all` takes every Wrekin session, those an earlier daemon left included. Worktrees stay for `wrekin cleanup` |
| `wrekin cleanup` | Remove the tmux sessions, worktrees and clones Wrekin created (`--dry-run`, `--yes`); never a repository that existed before Wrekin |
| `wrekin repos` | List the repository → checkout mappings, declared and learned, and whether each still holds (`--json` available) |
| `wrekin repos forget <repository>` | Forget a learned mapping (a declared pin lives in `settings.json`) |
| `wrekin repos pin <repository>` | Print the `settings.json` and Home Manager snippets that declare a learned mapping; writes nothing |
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
    "autoDiscover": true,
    "repositories": {
      "plmercereau/wrekin": "~/projects/wrekin"
    }
  },
  "teleport": {
    "terminal": "tmux",
    "maxConcurrent": 4,
    "remoteControl": true
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
- `teleport.remoteControl` (default `true`): start the teleported session
  with Claude Code's Remote Control (`--remote-control "<node> <session>"`),
  so you continue it from claude.ai/code or the Claude app under that name,
  as well as with `wrekin attach` on the machine. The Claude conversation
  that asked for the teleport keeps running in the cloud on its own; it
  cannot act on the machine.
- `workspace.repositories` (optional): pins a repository to its checkout.
  Keys take any form a teleport accepts (`owner/name` for GitHub, an
  `https://` or `ssh://` URL, `git@host:owner/name.git`); values are
  directories (`~` is expanded) that must lie under an allowed directory and
  hold a checkout whose `origin` is that repository.

Where a repository is checked out is either **declared** or **learned**, like
`ssh_config` and `known_hosts`. The declared pins are yours: they live in
`settings.json`, which Wrekin never writes (only `wrekin auth`, and only to
create it), so it may be managed read-only by Nix. The learned mappings are
the daemon's: `~/.local/state/wrekin/repositories.json` (mode 0600) records the
checkouts the discovery scan found and the clones you approved. A teleport
uses the declared pin first, and fails if the pin does not hold (an explicit
decision is never skipped silently); then the learned mapping, dropped if the
checkout moved or changed origin; then the scan; then it proposes a clone.
`wrekin repos` shows both kinds, `wrekin repos forget` drops a learned one and
`wrekin repos pin` prints what to add to the settings to make a learned one
permanent. `wrekin cleanup` forgets the mapping of a clone it deletes.

Other files, all local to the machine: the node identity (private key) in
`~/.config/wrekin/identity.json` (mode 0600), jobs in
`~/.local/state/wrekin/jobs.json`, the learned repository mappings in
`~/.local/state/wrekin/repositories.json`, the log in `~/.local/state/wrekin/wrekin.log`
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
cloud/              the Cloudflare Worker: TanStack Start, tRPC, Better Auth, Durable Object, MCP tools
protocol/           PROTOCOL.md, the node protocol (normative for both halves)
nix/                Nix package and Home Manager module (flake.nix at the root)
packaging/          how releases, Homebrew and the Worker deploy are produced
docs/               design spec, implementation plan, feasibility notes
.github/workflows/  ci.yml, release.yml (the Worker is deployed by Workers Builds, not by Actions)
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) to run both halves locally.

## Licence

[MIT](LICENSE), Pierre-Louis Mercereau, 2026.
