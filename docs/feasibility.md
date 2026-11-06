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

The sections below are the detailed notes taken while implementing the Go agent
(macOS, Darwin 25.5 arm64, tmux 3.6a, Claude Code 2.1.318); the table above is
the summary.

## P2 — Teleport inside tmux

### What `claude` says about itself

```
$ claude --version
2.1.318 (Claude Code)

$ claude --help | grep -A2 -- --teleport
  --teleport [session]                  Resume a teleport session, optionally
                                        specify session ID
  --tmux                                Create a tmux session for the worktree
                                        (requires --worktree). Uses iTerm2 ...
```

- `--teleport` takes an optional session id. Without one Claude Code presumably
  offers a picker; Wrekin always passes the id it received, so the picker is
  never involved.
- Claude Code has its own `--tmux` flag, tied to `--worktree`. Wrekin does not
  use it: the daemon creates the tmux session itself so that the session name
  (`wrekin-<shortId>`) and the working directory are under its control, and
  so that the behaviour does not depend on a terminal emulator (the flag
  mentions iTerm2).

### The detached probe

Procedure (what the plan asks for): in a scratch git repository, run
`claude --teleport session_00000000000000000000000000` detached in tmux,
capture the pane after 10 s, kill the session.

The probe cannot run inside the sandbox this agent works in: creating a tmux
server socket is refused (`error creating .../tmux-501/... (Operation not
permitted)`, while tmux still exits 0 — see the note on tmux below). One run
outside the sandbox was possible:

- the tmux server started and `new-session -d` returned 0;
- after 10 s `display-message` and `capture-pane` answered `can't find pane:
  =wrekin-probe`, which was read at the time as the session having died.

CI later showed what that error really was: the commands that take a
target-pane (`capture-pane`, `display-message`, `set-option`) read `=name` as
a window or pane name, not as an exact session name, and fail with exactly
that message while the session is alive; the exact-session form for them is
`=name:` (trailing colon). The session-taking commands (`has-session`,
`kill-session`, `attach-session`) do accept `=name`. The agent uses the right
form for each (`internal/terminal/tmux.go`, `target` and `paneTarget`), and
the tmux tests now run green on Linux (tmux 3.4) and macOS (tmux 3.8) in CI.
The probe below is corrected accordingly; the lines Claude Code prints for an
invalid id and the shape of its prompt are **still not recorded**: rerun the
script by hand (outside any sandbox) and paste the capture here.

```sh
#!/bin/sh
# P2 probe: detached claude --teleport with a bogus id; capture; kill.
set -u
for v in $(env | grep -o '^CLAUDE[A-Z_]*'); do unset "$v"; done   # no nested-session markers
PROBE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/wrekin-p2-XXXXXX")
cd "$PROBE_DIR" && git init -q -b main . && printf 'probe\n' > README.md \
  && git -c user.email=p@x -c user.name=p add README.md \
  && git -c user.email=p@x -c user.name=p commit -q -m init
cat > "$PROBE_DIR/probe-cmd.sh" <<'CMD'
#!/bin/sh
claude --teleport session_00000000000000000000000000
echo "[probe] claude exited with status $?"
sleep 30
CMD
SOCK=wrekin-probe-$$
tmux -L "$SOCK" -f /dev/null new-session -d -s wrekin-probe -c "$PROBE_DIR" "sh $PROBE_DIR/probe-cmd.sh" \
tmux -L "$SOCK" set-option -t '=wrekin-probe:' remain-on-exit on
sleep 10
tmux -L "$SOCK" display-message -p -t '=wrekin-probe:' '#{pane_current_command} dead=#{pane_dead}'
tmux -L "$SOCK" capture-pane -p -t '=wrekin-probe:' -S -40
tmux -L "$SOCK" kill-server
rm -rf "$PROBE_DIR"
```

### What the agent does meanwhile

The readiness detection is implemented as the plan specifies and is covered
by unit tests with a fake terminal:

- the session is launched with `tmux new-session -d -s wrekin-<shortId> -c
  <workdir> '<claude> --teleport <id>'`, its existence is checked with
  `has-session`, then a second call sets `destroy-unattached off` on it (best
  effort), so a user configuration that destroys unattached sessions cannot
  kill it; chained in the same invocation as `new-session`, that `set-option`
  answered "no such session" in CI;
- every 2 s for up to 120 s the daemon checks that the session exists and
  that `#{pane_current_command}` is not a shell (two consecutive "stopped"
  polls, or a vanished session, mean `harness_exited`), then matches the
  last 40 pane lines against `(?m)\? for shortcuts|❯|>\s*$`;
- a match is `ready{confirmed: true, detail: "prompt visible"}`; a timeout
  with the process still running is `ready{confirmed: false}` with a detail
  sentence, the spec's "uncertain outcome".

The pattern comes from the known Claude Code prompt (`❯`, the `>` input
line, the `? for shortcuts` hint) and must be confirmed against the probe
capture once it exists; it lives in one place,
`internal/harness/claude/claude.go`.

### A note on tmux exit codes

tmux can print an error (`error creating <socket> (Operation not
permitted)`) and still exit 0. `Terminal.Launch` therefore verifies with
`has-session` that the session exists after `new-session` and reports the
stderr text otherwise; the tmux tests skip, with that text, when the server
socket cannot be created.

## P3 — Git isolation and trust

### `.claude/worktrees`

Claude Code's own `--worktree` feature was observed in three local
repositories:

- worktrees live in `<repo>/.claude/worktrees/<name>`, with branches such as
  `claude/<name>` or `worktree-<name>`;
- Claude Code adds `.claude/worktrees/` and `**/.claude/worktrees/` to
  `.git/info/exclude` under the comment `# Claude Code worktrees (local
  only)`, so the directory never shows up as untracked work.

Wrekin follows the same convention and prepares the worktree itself (the
"manual preparation" branch of the spec): `<checkout>/.claude/worktrees/
wrekin-<shortId>` on branch `wrekin/<shortId>`, created from the checkout's
HEAD with `git worktree add -b` (or reusing the branch when it exists, which
is what a relaunch does), plus `.claude/worktrees/` appended to
`.git/info/exclude` when absent. The combination `--worktree --teleport` was
not exercised: it is interactive, and Wrekin does not need it since the
worktree exists before `claude` starts.

### Trust (`~/.claude.json`)

Observed on this machine (values not recorded, only the shape):

- the file is a JSON object (122 top-level keys here) with a `projects`
  object keyed by the absolute directory path;
- every project entry (45 here) carries `hasTrustDialogAccepted` (boolean),
  next to keys such as `allowedTools`, `mcpServers`, `enabledMcpjsonServers`,
  `hasClaudeMdExternalIncludesApproved` and session statistics.

`internal/harness/claude/trust.go` reads
`projects[<dir>].hasTrustDialogAccepted` and, on `Trust(dir)`, sets it to
`true` after copying the file to `~/.claude.json.wrekin-backup`; every other
value is preserved byte for byte as a JSON value, and the write is refused
(`ErrUnrecognisedClaudeJSON`) when its top level is not an object or its
`projects` is not an object. A missing file is created (mode 0600) with the
one entry, and a file without `projects` gains it: both are what a machine
where Claude Code never opened a project looks like (observed on a fresh
NixOS host on 2026-11-06, where `claude auth status` writes a
`~/.claude.json` with `firstStartTime`, `migrationVersion`, `userID` and no
`projects`). `CLAUDE_CONFIG_DIR`, when set for the daemon, moves the file to
`$CLAUDE_CONFIG_DIR/.claude.json`, as for Claude Code itself. This remains a version-sensitive
integration: a Claude Code release that moves the trust flag would make
`Trusted()` return false and the job would ask for approval again, which is
the safe failure.

### Open items

- P2: capture the pane of `claude --teleport <invalid id>` and of a valid
  teleport; confirm or adjust the ready pattern and the `harness_exited`
  interpretation.
- P3: confirm that Claude Code honours `hasTrustDialogAccepted` for a
  worktree path (as opposed to the checkout path) without re-asking.

## MVP success criterion

From a Claude Code Cloud session on mobile or web, an authenticated user can
request a teleport to a connected node. After the confirmations a new
repository may need, they find the same Claude Code session on the target
machine through tmux, with no inbound connection, no physical access and no
local secret sent to the mediator. P1 and P4 above are the parts of this
criterion that have not been exercised yet.
