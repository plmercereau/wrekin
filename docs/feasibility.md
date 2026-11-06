# Feasibility notes

Observations recorded while implementing Track A (the Go agent) on a macOS
machine (Darwin 25.5, arm64, tmux 3.6a, git, Claude Code 2.1.318). P1 and P4
belong to the cloud track and are not covered here.

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
- after 10 s the session no longer existed (`can't find pane: =wrekin-probe`),
  although the command chain ended with `sleep 30`;
- no pane content could be captured, so the lines Claude Code prints for an
  invalid id and the shape of its prompt are **not recorded yet**.

The command string of that run used `$?` inside the tmux command (the user
shell is zsh, so that is probably not the cause) and the user's tmux
configuration sets none of `destroy-unattached`, `default-command`,
`remain-on-exit` or hooks. The cause of the early exit is therefore unknown;
a second run with the improved script below was not permitted. **P2 is
open**: rerun the script by hand (outside any sandbox) and paste the capture
here.

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
  \; set-option -t wrekin-probe remain-on-exit on
sleep 10
tmux -L "$SOCK" display-message -p -t =wrekin-probe '#{pane_current_command} dead=#{pane_dead}'
tmux -L "$SOCK" capture-pane -p -t =wrekin-probe -S -40
tmux -L "$SOCK" kill-server
rm -rf "$PROBE_DIR"
```

### What the agent does meanwhile

The readiness detection is implemented as the plan specifies and is covered
by unit tests with a fake terminal:

- the session is launched with `tmux new-session -d -s wrekin-<shortId> -c
  <workdir> '<claude> --teleport <id>'` and, in the same tmux call,
  `set-option -t =wrekin-<shortId> destroy-unattached off`, so a user
  configuration that destroys unattached sessions cannot kill it;
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
(`ErrUnrecognisedClaudeJSON`) when the file is missing or its top level is
not an object with a `projects` object. This remains a version-sensitive
integration: a Claude Code release that moves the trust flag would make
`Trusted()` return false and the job would ask for approval again, which is
the safe failure.

### Open items

- P2: capture the pane of `claude --teleport <invalid id>` and of a valid
  teleport; confirm or adjust the ready pattern and the `harness_exited`
  interpretation.
- P3: confirm that Claude Code honours `hasTrustDialogAccepted` for a
  worktree path (as opposed to the checkout path) without re-asking.
