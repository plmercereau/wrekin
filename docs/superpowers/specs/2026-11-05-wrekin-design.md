# Wrekin — Design Specification v1.0

> To all agents round the Wrekin.

Status: proposal submitted for validation (implemented as the MVP in this repository).
Date: 5 November 2026.
Scope: MVP — Claude Code only.

## 1. Vision

Wrekin transfers a Claude Code Cloud session to a Linux or macOS machine the
user owns, so the session continues in a local terminal.

The teleport is triggered from Claude Mobile, Web or Desktop, through a
remote MCP server.

The user does not need to be in front of the target machine. The machine runs
a user-level daemon that keeps a permanent outbound connection to a Cloudflare
mediator.

### Goals

- Teleport on demand, with no SSH access and no inbound port.
- Simple installation on Linux and macOS, including headless machines.
- Discovery of the machines attached to a user account.
- No machine secrets on the mediator.
- Existing Git work is preserved.
- Persistent Claude Code sessions, recoverable through tmux.
- An architecture that can host other harnesses later.

### Out of scope

- Remote interactive terminal through MCP or a browser.
- Arbitrary shell command execution.
- Two-way synchronisation of Claude sessions.
- Deferred commands for disconnected machines.
- Harnesses other than Claude Code in the MVP.
- Automatic cleanup of sessions and directories.

## 2. Architecture choice

Three approaches were considered:

| Approach | Advantage | Drawback |
|---|---|---|
| Vercel + Redis | Reuses Vercel | Extra coordination between instances |
| Cloudflare Workers + Durable Objects | Per-user routing and persistent connections | Dependency on Cloudflare |
| P2P connections | Smaller mediator | Signalling, NAT and discovery are harder |

Chosen: Cloudflare Workers + Durable Objects.

### Cloud component

- TypeScript on Cloudflare Workers (workerd).
- Bun for development and dependency management.
- Hono for the HTTP APIs.
- A remote MCP server.
- Better Auth with GitHub OAuth and Google OIDC.
- Cloudflare D1 for the data authentication needs.
- One logical Durable Object per Wrekin account, holding the active
  connections and routing commands.

### Local agent

- A standalone binary written in Go.
- Runs as the Linux/macOS user.
- A `systemd --user` service on Linux.
- A launchd LaunchAgent on macOS.
- A persistent outbound WebSocket connection with automatic reconnection.
- Manages Git, Claude Code, tmux and the local jobs.
- Persistent state lives on the machine only.

The boundary between Go and Cloudflare is a versioned protocol, independent
of Claude Code.

## 3. Distribution and configuration

The Go binary is distributed through:

- GitHub Releases: Linux/macOS, amd64/arm64.
- Homebrew: a formula and `brew services`.
- A Nix flake: package, app and Home Manager module.

GoReleaser builds and publishes the binaries.

### CLI

```
wrekin auth
wrekin start
wrekin stop
wrekin status
wrekin sessions
wrekin attach <session>
wrekin logs
wrekin cleanup
wrekin logout
```

### Configuration

`~/.config/wrekin/settings.json`

```json
{
  "name": "workstation",
  "relay": {
    "url": "wss://bridge.example.com/connect"
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

Repository → directory associations are optional. Private keys,
authentication identifiers and job data are stored separately.

## 4. Identity and security

### User authentication

GitHub or Google are the identity providers, through Better Auth.

The MCP server uses OAuth to authenticate Claude against Wrekin. GitHub and
Google identities can be explicitly linked to one Wrekin account.

### Node enrolment

1. `wrekin auth` generates a local Ed25519 key pair.
2. A temporary pairing code is displayed.
3. The user opens the verification URL on another device.
4. They sign in with GitHub or Google.
5. They approve the association of the node and its public key.
6. The daemon then signs authentication challenges with its private key.

The private key never leaves the node.

### Trust model

Cloudflare can authenticate users and route commands. It holds no secret
that would open a local Claude session directly, reach SSH keys or decrypt
local data.

The daemon always enforces the path, operation and concurrency restrictions.

Accepted trade-off: a teleport runs automatically, without a second
authorisation factor per command. A compromised mediator could therefore
trigger the operations the protocol allows, within the limits enforced
locally.

### Confidentiality

Cloudflare never receives:

- The nodes' private keys.
- Local Claude Code or Git credentials.
- Raw terminal output.
- Full local logs.

It necessarily receives some authentication and routing metadata, and the
MCP arguments a teleport needs, notably the session identifier.

OAuth and Better Auth secrets are infrastructure secrets hosted on
Cloudflare.

## 5. Teleport journey

1. In Claude, the user asks to teleport the session to a machine.
2. Claude calls the MCP tool `list_nodes` if a destination must be chosen.
3. Claude passes the cloud session identifier to the `teleport` tool.
4. Cloudflare checks the user's identity and forwards the command to the
   connected node.
5. The daemon checks the command against its local policy.
6. It identifies the repository and prepares an allowed directory.
7. If the repository is new, the user must confirm the clone and the trust
   granted to the repository, through Claude.
8. The daemon launches `claude --teleport` in a tmux session.
9. The job becomes `ready` once the teleport is confirmed.
10. The user later finds the session with `wrekin attach`.

Nothing requires an inbound connection to the target machine.

## 6. Git and workspaces

Only directories under explicitly allowed roots can be used.

The daemon resolves canonical paths and prevents escapes through symbolic
links or directory changes.

### Existing repositories

Wrekin looks for available checkouts, without modifying a repository that
holds unprotected local work.

An isolated worktree is preferred, but the `--worktree` / `--teleport`
combination must be tested before the strategy is fixed.

If needed, Wrekin prepares an isolated Git environment itself.

### Absent repositories

Wrekin proposes a clone path under an allowed root.

The user confirms the clone from Claude. Git uses local authentication
mechanisms only (SSH, credential helper, ...).

### Claude Code trust

Already approved repositories can be used automatically.

New repositories need an explicit confirmation from Claude, after which
Wrekin prepares the local approval of the directory.

This confirmation prevents accidental approvals; it is not an authorisation
independent of a compromised Cloudflare mediator.

No global bypass of Claude Code permissions is allowed.

## 7. Jobs and sessions

Each teleport is an asynchronous job managed locally.

### States

```
accepted
preparing
needs_attention
launching
ready
failed
```

`teleport` returns a job identifier quickly. `get_teleport_status` follows
its progress.

States are persisted on the node and recovered after a daemon restart.

### Idempotence

A teleport is identified by `(nodeId, harness, cloudSessionId)`.

Repeated requests return the existing session instead of creating a new one.

If a previously created tmux session has disappeared, Wrekin says so and asks
for an explicit relaunch.

### Concurrency

Four simultaneous teleport preparations by default, configurable.

No limit on the number of tmux sessions already active.

### Offline nodes

A request to a disconnected node fails immediately. No command is queued on
Cloudflare.

An operation already accepted continues locally through a network
interruption.

### Retention

No automatic deletion.

The tmux sessions, clones and worktrees Wrekin creates stay until an
explicit cleanup.

Repositories that existed before Wrekin are never deleted by the cleanup.

## 8. MCP interface

The MVP exposes:

| Tool | Purpose |
|---|---|
| `list_nodes` | List the machines and their connection state |
| `teleport` | Prepare and launch a teleport |
| `get_teleport_status` | Read a job's state |
| `approve_repository` | Explicitly confirm the use of a new repository |

MCP responses are structured and carry no raw logs.

The protocol between MCP and daemon is designed for several harness
adapters. Only the Claude Code adapter is implemented.

## 9. Resilience and error handling

- WebSocket reconnection with exponential backoff and jitter.
- Node authentication after every reconnection.
- Local command de-duplication.
- No automatic double launch.
- Local logging with stable error codes.
- No indefinite block on an interactive Git prompt.
- Session state preserved across restarts.
- Explicit handling of uncertain outcomes after a broken connection.

## 10. Feasibility checks before full development

Four feasibility tests precede the complete development.

**P1 — Claude Cloud session identifier.** Check that an MCP call can obtain
automatically an identifier compatible with `claude --teleport`, in
particular from Claude Mobile and Web. The identifier of an ordinary Claude
conversation is not enough.

**P2 — Teleport inside tmux.** Check how `claude --teleport` behaves in a
detached terminal and determine how to tell a successful launch from a
session that is really ready.

**P3 — Git isolation and trust.** Test `--worktree` with `--teleport`, the
manual preparation of a worktree and the trust mechanism for new
repositories. Any manipulation of `~/.claude.json` is a version-sensitive
integration, not assumed stable.

**P4 — Authentication.** Validate the Better Auth / D1 / Cloudflare Workers
integration, the MCP authorisation server and the Ed25519 pairing through
Device Authorization.

### MVP success criterion

From a Claude Code Cloud session on mobile or web, an authenticated user can
request a teleport to a connected node. After the confirmations a new
repository may need, they find the same Claude Code session on the target
machine through tmux, with no inbound connection, no physical access and no
local secret sent to the mediator.

## 11. Decisions left to the implementation

The following need no new brainstorming:

- Exact Go and TypeScript libraries.
- WebSocket protocol format and versioning.
- Local job storage.
- Claude Code availability detection.
- Final Git isolation implementation, after the tests.
- Homebrew and Nix packaging.
- Test, CI and release strategy.

The choices must respect the security constraints and behaviours defined in
this document.

---

End of brainstorming — awaiting design validation. After approval, the next
step is a Superpowers implementation plan, starting with the four
feasibility tests.
