# Feasibility checks

The design spec (`docs/superpowers/specs/2026-11-05-wrekin-design.md`,
section 10) names four questions that must hold for Wrekin to work. This file
tracks their state. "Verified locally" is what was observed on a development
machine (macOS, Claude Code 2.1.318) while building the MVP; "Remains" is
what can only be checked from Claude Mobile/Web or on the deployed Worker.

| Item | Question | Verified locally | Remains |
|---|---|---|---|
| P1 | Can an MCP call obtain, automatically, a session identifier compatible with `claude --teleport`, from Claude Mobile and Web? | The MCP `teleport` tool accepts both a `session_…` id and a `https://claude.ai/code/session_…` URL (`parseCloudSessionId` in `cloud/src/mcp`), and its description tells Claude to pass its own Claude Code Cloud session, not a chat URL. Claude Code sessions in this repository carry a `claude.ai/code/session_…` URL. | Confirm, from Claude Mobile and from Claude Web, that the model actually knows and passes its Claude Code Cloud session id or `claude.ai/code` URL to the `teleport` tool without the user typing it. If it does not, the fallback is the user pasting the session URL. |
| P2 | Does `claude --teleport` work in a detached terminal, and how do we tell a launched process from a session that is really ready? | `claude --help` shows `--teleport [session]` ("Resume a teleport session, optionally specify session ID"), `-w, --worktree [name]` and `--tmux` (requires `--worktree`; iTerm2 panes by default, `--tmux=classic` for plain tmux). The daemon therefore drives tmux itself (`tmux new -d`) and does not use `--tmux`. Readiness is a prompt pattern seen in `tmux capture-pane`; a process still running after 120 s with no prompt is reported `ready.confirmed = false`. | Record the actual pane output of a teleport in a detached session, including how an invalid id fails (see "Observed in the P2 probe" below). |
| P3 | Can the teleport run in an isolated git worktree, and can the Claude Code trust for a new repository be prepared safely? | Claude Code puts its own worktrees under `<repo>/.claude/worktrees/<name>` and adds `.claude/worktrees/` (and `**/.claude/worktrees/`) to `<repo>/.git/info/exclude`; Wrekin follows the same convention (`<repo>/.claude/worktrees/wrekin-<shortID>`, branch `wrekin/<shortID>`, exclude entry ensured). Trust is `projects["<absolute dir>"].hasTrustDialogAccepted` in `~/.claude.json` (a boolean, present on every project entry observed, next to keys such as `allowedTools`, `mcpServers`, `lastSessionId`). It is treated as a version-sensitive integration: the file is edited only by `internal/harness/claude/trust.go`, after a backup, and refused if it does not parse as an object with a `projects` object. | Observe a real teleport inside a worktree created by Wrekin (not by `--worktree`) and confirm that Claude Code opens the session there without asking for trust again. |
| P4 | Does the authentication stack work end to end: Better Auth on D1 and Cloudflare Workers, the MCP authorisation server, Ed25519 pairing through device authorisation? | The Worker tests (`cloud/test`) cover the OAuth metadata documents, the device code endpoints, node registration and the signed handshake with a key pair generated in the test, against local D1 and Durable Objects (`@cloudflare/vitest-pool-workers`). | Validate on the deployed Worker with Claude's connector: add `https://wrekin.platyplus.io/mcp` in Claude, complete the OAuth sign-in and consent, see `list_nodes` answer; pair a real machine with `wrekin auth` through `/device`. Note whether Claude's connector needs Dynamic Client Registration enabled on the MCP plugin. |

## Observed in the P2 probe

_To be filled by Track A (plan Task A9, step 6): run
`tmux new -d -s wrekin-probe 'claude --teleport session_00000000000000000000000000'`
in a temporary git repository, capture the pane after 10 seconds, record the
lines observed (how an invalid id fails, what the prompt looks like when a
valid one is used), then kill the session._

## MVP success criterion

From a Claude Code Cloud session on mobile or web, an authenticated user can
request a teleport to a connected node. After the confirmations a new
repository may need, they find the same Claude Code session on the target
machine through tmux, with no inbound connection, no physical access and no
local secret sent to the mediator. P1 and P4 above are the parts of this
criterion that have not been exercised yet.
