# Wrekin MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go agent and a Cloudflare Worker that, together, let a user teleport a Claude Code Cloud session onto one of their own machines from Claude through a remote MCP server, with no inbound connection and no machine secret on the mediator.

**Architecture:** Three independent tracks share one versioned contract, `protocol/PROTOCOL.md`. Track A is the Go agent (`cmd/wrekin`, `internal/*`): a daemon holding one outbound WebSocket, a job state machine persisted on disk, and adapters for Git, tmux and Claude Code. Track B is the Worker (`cloud/`): Hono + Better Auth (GitHub, Google, device authorization, MCP OAuth provider) on D1, one Durable Object per account that owns the node sockets and routes commands, and the MCP tools. Track C is distribution: CI, GoReleaser, Homebrew tap config, Nix flake with a Home Manager module. Track D is the platyplus/infrastructure PR (OpenTofu for D1 + the custom domain, runbook).

**Tech Stack:** Go 1.24 (`github.com/coder/websocket`, `github.com/spf13/cobra`, stdlib `crypto/ed25519`, `log/slog`); Bun + TypeScript, Hono 4, `better-auth` 1.7 with `@better-auth/mcp`, `kysely` + `kysely-d1`, `@hono/mcp` + `@modelcontextprotocol/sdk`, `zod`, Wrangler 4, Vitest with `@cloudflare/vitest-pool-workers`; GoReleaser; Nix flakes; OpenTofu with the Cloudflare provider 5.x.

**Spec:** `docs/superpowers/specs/2026-11-05-wrekin-design.md`. **Protocol:** `protocol/PROTOCOL.md` (normative for Tracks A and B).

## Global Constraints

- Everything written in this repository and in the infrastructure PR is in English: code, comments, docs, commits, PR text.
- Go module path `github.com/plmercereau/wrekin`; Go ≥ 1.24; no cgo; builds for linux/darwin × amd64/arm64.
- Cloud base URL in production: `https://wrekin.platyplus.io`; node relay URL `wss://wrekin.platyplus.io/connect`; MCP endpoint `https://wrekin.platyplus.io/mcp`; the OAuth `resource` equals the MCP endpoint URL.
- Config file `~/.config/wrekin/settings.json` with exactly the keys of spec §3; identity in `~/.config/wrekin/identity.json` (mode 0600); jobs in `~/.local/state/wrekin/jobs.json`; log in `~/.local/state/wrekin/wrekin.log`; control socket `~/.local/state/wrekin/wrekin.sock`. `XDG_CONFIG_HOME` / `XDG_STATE_HOME` override the two prefixes on both OSes.
- Defaults: `teleport.terminal = "tmux"`, `teleport.maxConcurrent = 4`, `workspace.autoDiscover = true`, `logging.level = "info"`, `relay.url = "wss://wrekin.platyplus.io/connect"`.
- Job states, error codes, message shapes: exactly `protocol/PROTOCOL.md`. Nothing sent to the relay contains raw terminal output, local log lines, Git credentials or paths outside the job's own `workdir`/`proposedPath`.
- Git never prompts: every git invocation runs with `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=/bin/false` (or `echo` on Windows-less builds, we only ship Unix), `GIT_SSH_COMMAND="ssh -o BatchMode=yes"`, and a 10-minute timeout for clones.
- Wrekin never deletes a repository it did not create; the job store records what Wrekin created (`createdClone`, `createdWorktree`, `tmuxSession`) and `wrekin cleanup` removes only those.
- Harness adapter boundary: `internal/harness.Harness` interface; only `claude-code` is implemented; nothing outside `internal/harness/claude` imports Claude specifics.
- `~/.claude.json` is edited only by `internal/harness/claude/trust.go`, only to set `projects[<dir>].hasTrustDialogAccepted = true`, with a backup written first and a refusal if the file does not parse as an object with a `projects` object.
- The Worker stores per node: id, account (Better Auth user id), name, public key, agent version, created/last-seen timestamps. Never a private key, token or session output.
- Secrets on the Worker are Wrangler secrets: `BETTER_AUTH_SECRET`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`. `BASE_URL` is a plain var.

## Review Focus

Inputs the spec implies but no happy path exercises; each has a test in the owning task.

1. A `directory` hint that resolves through a symlink to outside every allowed root must be refused with `path_escapes_root`, even when the un-resolved string starts with an allowed root. (Task A4)
2. A second `teleport` for the same `cloudSessionId` while the first is still `preparing` must return the same job id and start nothing. (Task A7)
3. A `teleport` for a job already `ready` whose tmux session no longer exists must answer `session_gone` and not launch until `relaunch: true`. (Task A7)
4. A node whose socket drops mid-preparation must finish the preparation locally and answer `get_teleport_status` correctly after reconnecting. (Task A8: the job store is the source of truth, the relay connection is not consulted by the job runner.)
5. A user with two Wrekin nodes of the same name is impossible (`UNIQUE(account_id, name)`), and a `teleport` naming a node of another account answers `unknown_node`, never `node_offline`. (Task B4, B6)

---

## Repository layout

```
cmd/wrekin/main.go                 entry point: cli.Execute()
internal/cli/                      cobra commands (one file per command)
internal/config/                   settings.json: load, defaults, validation, path expansion
internal/paths/                    XDG-style directories for config/state
internal/identity/                 Ed25519 key pair, identity.json, signing
internal/protocol/                 Go types + JSON for PROTOCOL.md
internal/relay/                    WebSocket client: handshake, ping, reconnect, command dispatch
internal/workspace/                allowed roots, canonical paths, checkout discovery, clone path proposal
internal/gitx/                     git commands: remotes, dirty check, clone, worktree add, info/exclude
internal/harness/                  Harness interface, registry
internal/harness/claude/           Claude Code adapter: detect, trust, launch command, readiness probe
internal/terminal/                 Terminal interface + tmux implementation
internal/jobs/                     Job model, store (jobs.json), runner (state machine), idempotence
internal/daemon/                   wires config, identity, relay, jobs; command handlers
internal/control/                  unix socket server/client for the CLI (status, sessions, cleanup)
internal/service/                  systemd --user unit, launchd plist: install/start/stop
internal/pairing/                  device-authorization client for `wrekin auth`
cloud/                             Bun workspace: the Worker
cloud/src/index.ts                 Hono app, routes
cloud/src/env.ts                   Env bindings type
cloud/src/auth.ts                  Better Auth factory per request
cloud/src/db/                      Kysely types, migrations (SQL), node repository
cloud/src/protocol/                zod schemas of PROTOCOL.md
cloud/src/hub/account-hub.ts       Durable Object
cloud/src/mcp/                     McpServer factory + tools
cloud/src/pages/                   HTML pages (home, login, device, consent, nodes)
cloud/src/api/nodes.ts             node registration/unregistration
cloud/test/                        vitest (workers pool)
protocol/PROTOCOL.md               the contract
packaging/                         notes; GoReleaser writes the Homebrew formula
nix/                               Home Manager module
flake.nix                          package, app, module, devShell
.goreleaser.yaml
.github/workflows/ci.yml, release.yml, deploy-cloud.yml
docs/                              spec, plan, feasibility notes, runbook for the site in the infra repo
```

---

# Track A — Go agent

### Task A1: Module, paths, config

**Files:**
- Create: `go.mod`, `cmd/wrekin/main.go`, `internal/paths/paths.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/paths/paths_test.go`

**Interfaces:**
- Produces: `paths.ConfigDir() string`, `paths.StateDir() string`, `paths.SettingsFile()`, `paths.IdentityFile()`, `paths.JobsFile()`, `paths.LogFile()`, `paths.SocketFile()`; `config.Settings` struct mirroring spec §3 (`Name`, `Relay.URL`, `Workspace.AllowedDirectories []string`, `Workspace.DefaultDirectory`, `Workspace.AutoDiscover bool`, `Teleport.Terminal`, `Teleport.MaxConcurrent int`, `Logging.Level`); `config.Load(path string) (Settings, error)` (missing file → defaults, with `Name` = hostname); `config.Default() Settings`; `(Settings) Validate() error`; `config.ExpandHome(p string) string`; `(Settings) HTTPBaseURL() string` (wss→https, strip `/connect`).

- [ ] **Step 1: Failing tests** — `TestLoadMissingFileGivesDefaults` (relay URL, maxConcurrent 4, terminal tmux, level info, name = hostname), `TestLoadExpandsTildeInDirectories`, `TestValidateRejectsZeroMaxConcurrent`, `TestValidateRejectsNonWSSRelay` (`http://` refused, `ws://localhost` accepted only when host is localhost/127.0.0.1), `TestHTTPBaseURL` (`wss://wrekin.platyplus.io/connect` → `https://wrekin.platyplus.io`), `TestPathsHonourXDG`.
- [ ] **Step 2: Run** `go test ./internal/config/ ./internal/paths/` → FAIL (undefined).
- [ ] **Step 3: Implement.** `go.mod` module `github.com/plmercereau/wrekin`, `go 1.24`. `main.go` calls `cli.Execute()` (stub `internal/cli` with a root command so it compiles).
- [ ] **Step 4: Run tests** → PASS. `go vet ./...` clean.
- [ ] **Step 5: Commit** `feat(agent): module, paths and settings loading`.

### Task A2: Identity

**Files:** `internal/identity/identity.go`, `identity_test.go`

**Interfaces:**
- Produces: `identity.Identity{NodeID, AccountID, Name, PublicKey ed25519.PublicKey, PrivateKey ed25519.PrivateKey, RelayURL string, CreatedAt time.Time}`; `identity.Generate(name string) (*Identity, error)`; `identity.Load(path) (*Identity, error)` (`ErrNotPaired` when missing); `(*Identity) Save(path) error` (0600, atomic rename); `(*Identity) Sign(nodeID, nonce string) string` returning base64 of Ed25519 over `"wrekin-node-auth-v1\n"+nodeID+"\n"+nonce`; `(*Identity) PublicKeyBase64() string`; `(*Identity) Fingerprint() string` (first 16 hex chars of SHA-256 of the raw public key, grouped by 4 with `:`); `identity.Verify(pubB64, nodeID, nonce, sigB64) bool` (used by tests and by the fake relay).

- [ ] **Step 1: Tests** — `TestGenerateSaveLoadRoundTrip` (file mode 0600), `TestSignVerifiesWithProtocolMessage` (verify with `ed25519.Verify` over the exact concatenation), `TestVerifyRejectsTamperedNonce`, `TestLoadMissingIsErrNotPaired`.
- [ ] **Step 2: FAIL. Step 3: Implement. Step 4: PASS. Step 5: Commit** `feat(agent): Ed25519 node identity`.

### Task A3: Protocol types

**Files:** `internal/protocol/protocol.go`, `protocol_test.go`

**Interfaces:**
- Produces: constants `Version = 1`; message structs `Hello`, `Challenge`, `Auth`, `Welcome`, `Command`, `Result`, `Ping`, `Pong`, `ErrorMessage` with `json` tags exactly as PROTOCOL.md; `Envelope{V int; Type string}` + `protocol.Decode([]byte) (any, error)` returning the typed message by `type` (unknown type → `UnknownMessage`); `Job`, `Repository`, `Session`, `Attention`, `Ready`, `JobError` structs; `State` string type with the six constants; error-code constants (`CodeNodeOffline = "node_offline"` … all of the table); `teleport` params struct `TeleportParams{Harness, CloudSessionID, Repository, Directory string; Relaunch bool}`, `StatusParams{JobID}`, `ApproveParams{JobID string; Approved bool}`; `NewResult(id string, data any) Result`, `NewErrorResult(id, code, msg string) Result`.

- [ ] **Step 1: Tests** — `TestDecodeCommandTeleport` (the PROTOCOL.md example decodes with the right params), `TestJobJSONMatchesProtocolExample` (marshal the Job example from PROTOCOL.md, compare as normalised JSON: nulls present for absent sub-objects), `TestUnknownTypeIsIgnored`.
- [ ] **Steps 2–5**, commit `feat(agent): protocol v1 types`.

### Task A4: Workspace policy

**Files:** `internal/workspace/workspace.go`, `workspace_test.go`

**Interfaces:**
- Produces: `workspace.Policy{Roots []string (canonical, absolute); DefaultRoot string}`; `workspace.NewPolicy(allowed []string, defaultDir string) (*Policy, error)` (expands `~`, `EvalSymlinks`, roots must exist); `(*Policy) Resolve(p string) (string, error)` → canonical path or `ErrNotAllowed` / `ErrEscapesRoot` (symlink target outside root); `(*Policy) ProposeClonePath(repoName string) string` → `<DefaultRoot>/<repoName>`, suffixed `-2`, `-3` if taken; `workspace.RepoRef{Input, URL, Name string}`; `workspace.ParseRepository(input string) (RepoRef, error)` accepting `owner/name` (→ `https://github.com/owner/name`), `https://…`, `git@host:owner/name.git`, `ssh://…`; `workspace.SameRepository(a, b string) bool` (normalised: scheme-less host + path, `.git` stripped, case-insensitive host).

- [ ] **Step 1: Tests** — `TestResolveInsideRoot`, `TestResolveOutsideRootIsNotAllowed`, `TestResolveSymlinkEscapeIsRefused` (create `root/link → /tmp/elsewhere`, resolve `root/link/x` → `ErrEscapesRoot`), `TestResolveDotDotIsRefused` (`root/../other`), `TestParseRepositoryForms` (table of the four forms → same `URL` and `Name`), `TestSameRepositoryNormalises` (`git@github.com:a/b.git` ≡ `https://github.com/a/b`), `TestProposeClonePathAvoidsCollision`.
- [ ] **Steps 2–5**, commit `feat(agent): workspace policy and repository references`.

### Task A5: Git operations

**Files:** `internal/gitx/gitx.go`, `gitx_test.go` (tests create real repositories in `t.TempDir()` with the `git` binary; skip if absent)

**Interfaces:**
- Produces: `gitx.Runner` (wraps `exec.Command("git", …)` with the no-prompt env from Global Constraints and a context timeout); `gitx.IsRepo(dir) bool`; `gitx.TopLevel(dir) (string, error)`; `gitx.Remotes(dir) (map[string]string, error)`; `gitx.FindCheckouts(roots []string, repoURL string, maxDepth int) ([]string, error)` (walks each root to depth 3, skipping hidden dirs and `node_modules`, returns top-levels whose `origin` matches by `workspace.SameRepository`); `gitx.HasUncommittedWork(dir) (bool, error)` (`status --porcelain` non-empty, untracked included); `gitx.Clone(ctx, url, dest string) error` (maps a credential prompt failure — exit with stderr containing `terminal prompts disabled` or `Permission denied (publickey)` — to `ErrPromptBlocked`); `gitx.AddWorktree(ctx, repo, path, branch string) error` (`git worktree add -b <branch> <path>` from HEAD; if the branch exists, `git worktree add <path> <branch>`); `gitx.RemoveWorktree(ctx, repo, path string) error`; `gitx.EnsureExcluded(repo, pattern string) error` appends to `.git/info/exclude` if absent.

- [ ] **Step 1: Tests** — `TestFindCheckoutsMatchesRemoteForms`, `TestHasUncommittedWorkSeesUntracked`, `TestCloneLocalPath` (clone from a local bare repo), `TestCloneWithoutCredentialsIsPromptBlocked` (clone `https://github.com/plmercereau/this-repo-does-not-exist-wrekin` with the no-prompt env; assert `ErrPromptBlocked` or a plain error, never a hang: wrap in a 60 s context and assert the call returned), `TestAddWorktreeAndExclude`.
- [ ] **Steps 2–5**, commit `feat(agent): git operations without prompts`.

### Task A6: Terminal (tmux) and harness (Claude Code)

**Files:** `internal/terminal/terminal.go`, `internal/terminal/tmux.go`, `tmux_test.go` (skip without `tmux`); `internal/harness/harness.go`, `internal/harness/claude/claude.go`, `internal/harness/claude/trust.go`, `trust_test.go`, `claude_test.go`

**Interfaces:**
- Produces (terminal): `terminal.Terminal` interface `{ Name() string; Available() error; Launch(ctx, session, workdir string, argv []string) error; Exists(session) (bool, error); Running(session) (bool, error) /* pane_current_command is not a shell */; Capture(session string, lines int) (string, error); Kill(session) error; List(prefix string) ([]string, error); AttachCommand(session) []string }`; `terminal.NewTmux() *Tmux`; `terminal.Lookup(name string) (Terminal, error)` (`tmux` only; else `terminal_unavailable`).
- Produces (harness): `harness.Harness` interface `{ ID() string; Detect() (harness.Info, error) /* Info{Available bool; Version string} */; Trusted(dir string) (bool, error); Trust(dir string) error; LaunchArgs(cloudSessionID string) []string; ReadyPattern() *regexp.Regexp; ExitedIsFailure() bool }`; `harness.Registry` with `Get(id) (Harness, error)`; `claude.New(claudeJSONPath string, binary string) *Claude` with `LaunchArgs(id) = []string{binary, "--teleport", id}`, `ReadyPattern()` matching `\? for shortcuts|❯|>\s*$` (multi-line), `Detect()` running `claude --version`.
- `trust.go`: `Trusted(dir)` reads `projects[dir].hasTrustDialogAccepted`; `Trust(dir)` writes the key after copying the file to `<file>.wrekin-backup`; errors `ErrUnrecognisedClaudeJSON` when the top level is not an object with a `projects` object.

- [ ] **Step 1: Tests** — `TestTmuxLaunchExistsCaptureKill` (launch `sh -c 'echo READY; sleep 30'`, `Exists` true, `Capture` contains `READY`, `Running` true, kill → `Exists` false); `TestTrustedReadsClaudeJSON`, `TestTrustWritesKeyAndBackup` (other keys preserved byte-for-byte as JSON values), `TestTrustRefusesUnrecognisedFile`, `TestLaunchArgs`, `TestReadyPatternMatchesPromptLines`.
- [ ] **Steps 2–5**, commit `feat(agent): tmux terminal and Claude Code harness adapter`.

### Task A7: Jobs — model, store, runner

**Files:** `internal/jobs/job.go`, `store.go`, `store_test.go`, `runner.go`, `runner_test.go`

**Interfaces:**
- Produces: `jobs.Job` (embeds `protocol.Job` plus local-only fields `CreatedClone bool`, `CreatedWorktree bool`, `CheckoutDir string`, `ApprovedAt *time.Time` — local fields carry `json:"-"`? No: they are persisted locally, so the store writes `jobs.Job` and the wire conversion `(*Job) Wire() protocol.Job` drops them); `jobs.Store` with `Open(path) (*Store, error)`, `Get(id)`, `FindBySession(harness, cloudSessionID) (*Job, bool)`, `Put(*Job) error` (atomic write of the whole file), `List() []*Job` newest first; `jobs.Runner` with `NewRunner(RunnerDeps) *Runner` where `RunnerDeps{Store; Policy *workspace.Policy; Git gitx.Runner; Term terminal.Terminal; Harness harness.Harness; MaxConcurrent int; ReadyTimeout time.Duration (default 120 s); Now func() time.Time; Logger *slog.Logger}`; `(*Runner) Teleport(ctx, protocol.TeleportParams) (*Job, *protocol.JobError)`; `(*Runner) Status(jobID)`; `(*Runner) Approve(jobID string, approved bool)`; `(*Runner) List()`; `(*Runner) Recover()` (on start: jobs left in `preparing`/`launching` → `failed` with `internal` and message "daemon restarted during preparation"; `ready` jobs whose tmux session is gone keep `ready` until the next request, which answers `session_gone`).
- State machine (the algorithm the tests pin):
  1. `Teleport`: validate params (`invalid_params`), both `repository` and `directory` absent → `repository_required`. Existing job for `(harness, cloudSessionId)`: if `ready`/`launching` and session exists → return it; if `ready` and session gone → mark `failed`/`session_gone`, return it unless `relaunch` → new job; if `failed` → new job only with `relaunch`; otherwise (accepted/preparing/needs_attention) return it. Then check harness and terminal availability, check `MaxConcurrent` (count of jobs in `preparing|launching`) → `concurrency_limit`. Create job `accepted`, persist, start goroutine `prepare(job)`, return.
  2. `prepare`: → `preparing`. If `directory`: `Policy.Resolve` → errors map to `directory_not_allowed`/`path_escapes_root`; if it is a git repo use it as checkout, else use it as workdir directly (no worktree). Else `ParseRepository`, `FindCheckouts` under roots; found → checkout = first; none → `needs_attention` with `proposedPath = ProposeClonePath(name)` and return.
  3. `Approve(true)`: → `preparing`, `Clone` into `proposedPath` (`clone_failed`/`git_prompt_blocked`), set `CreatedClone`, continue at 4. `Approve(false)` → `failed`/`not_approved`.
  4. Isolation: when checkout is a repo, worktree at `<checkout>/.claude/worktrees/wrekin-<shortID>` with branch `wrekin/<shortID>` (`shortID` = last 8 chars of the session id, lower-case), `EnsureExcluded(checkout, ".claude/worktrees/")`, set `CreatedWorktree`; failure → `worktree_failed`. `workdir` = worktree path.
  5. Trust: if `!Harness.Trusted(workdir)` and (job was approved by the user, or the checkout was already trusted) → `Harness.Trust(workdir)`; if not trusted and the checkout is not trusted either and no approval → `needs_attention` (kind `approve_repository`, message says the directory is not yet trusted by Claude Code) — the approval then trusts both.
  6. `launching`: `Term.Launch(ctx, "wrekin-<shortID>", workdir, Harness.LaunchArgs(id))`; `session` set; poll every 2 s up to `ReadyTimeout`: `Running` false → `failed`/`harness_exited`; `Capture(…, 40)` matches `ReadyPattern` → `ready{confirmed:true}`; timeout with still running → `ready{confirmed:false, detail:"the harness is running but its prompt was not seen within 120 s; attach to check"}`.
  Every transition updates `updatedAt` and persists before returning.

- [ ] **Step 1: Tests** with fakes (`fakeTerminal`, `fakeHarness`, a real `Store` in a temp dir, real git repos in temp dirs, `Policy` over a temp root): `TestTeleportRequiresRepositoryOrDirectory`, `TestTeleportFindsExistingCheckoutAndBecomesReady` (fake terminal reports the ready pattern), `TestTeleportUnknownRepositoryNeedsAttentionWithProposedPath`, `TestApproveClonesAndLaunches` (clone from a local bare repo via a `file://` URL: the test passes `repository` as that URL), `TestApproveDeclinedFails`, `TestRepeatTeleportReturnsSameJob` (Review Focus 2: start `prepare` blocked on a channel; second call returns same id; only one `Launch`), `TestReadySessionGoneAnswersSessionGoneThenRelaunch` (Review Focus 3), `TestConcurrencyLimit`, `TestWorktreeIsolationCreatesBranchAndExclude`, `TestUntrustedDirectoryNeedsAttentionThenTrustOnApprove`, `TestHarnessExitBeforeReadyFails`, `TestReadyTimeoutIsUnconfirmed`, `TestRecoverMarksInterruptedJobsFailed`, `TestStoreRoundTripKeepsLocalFields`.
- [ ] **Steps 2–5**, commit `feat(agent): teleport jobs, persisted state machine`.

### Task A8: Relay client

**Files:** `internal/relay/client.go`, `client_test.go`, `internal/relay/fakerelay_test.go` (an in-process relay built on `httptest.NewServer` + `coder/websocket` that performs the handshake with `identity.Verify` and can send commands and record results)

**Interfaces:**
- Produces: `relay.Handler` interface `{ Handle(ctx, protocol.Command) protocol.Result }`; `relay.Client` with `relay.New(relay.Options{URL string; Identity *identity.Identity; Name, AgentVersion string; Harnesses []protocol.HarnessInfo; Handler; Logger; Dial func(ctx, url) (*websocket.Conn, error) /* for tests */}) *Client`; `(*Client) Run(ctx) error` (loops: connect, handshake, serve, backoff; returns on ctx done); `(*Client) State() relay.State` (`Disconnected|Connecting|Connected`, plus `LastError`, `ConnectedSince`, `AccountID`); de-duplication cache of command ids (10 min) in the client, so the handler is never called twice for one id.
- Backoff: initial 1 s, ×2, cap 60 s, full jitter; reset on `welcome`; after close codes 4401/4404 wait the cap.
- Ping every `pingSeconds` from `welcome` (default 30), reconnect when no `pong` within 10 s.

- [ ] **Step 1: Tests** against the fake relay: `TestHandshakeSignsChallenge`, `TestCommandIsHandledAndAnswered`, `TestDuplicateCommandIdAnsweredOnce`, `TestReconnectsAfterServerClose` (server closes 1000; client is `Connected` again within 5 s with a test backoff of 10 ms), `TestAuthFailureWaitsCap` (server closes 4401; no reconnect attempt within 200 ms when cap set to 1 s), `TestPongTimeoutReconnects`.
- [ ] **Steps 2–5**, commit `feat(agent): relay client with signed handshake and reconnection`.

### Task A9: Daemon, control socket, pairing, service, CLI

**Files:** `internal/daemon/daemon.go`, `daemon_test.go`; `internal/control/{server.go,client.go,control_test.go}`; `internal/pairing/pairing.go`, `pairing_test.go` (httptest fake of the Better Auth device endpoints); `internal/service/{service.go,systemd.go,launchd.go,service_test.go}`; `internal/cli/{root.go,auth.go,start.go,stop.go,status.go,sessions.go,attach.go,logs.go,cleanup.go,logout.go,daemon.go}`; `cmd/wrekin/main.go`

**Interfaces:**
- `daemon.Run(ctx, settingsPath string) error`: loads config and identity (no identity → exits with "not paired, run `wrekin auth`"), opens the store, builds the runner, serves the control socket, runs the relay client with a `Handler` mapping command names to runner methods (`teleport`, `get_teleport_status`, `approve_repository`, `list_jobs`, `ping`; unknown → `invalid_params`), logs to `paths.LogFile()` (rotating by size 10 MB × 3) and stderr.
- `control`: JSON-RPC-ish over the unix socket: requests `status` → `{relay: State, nodeId, name, accountId, jobs: {counts by state}, version}`; `sessions` → `[]protocol.Job`; `cleanup {dryRun bool}` → list of what would be / was removed (tmux sessions, worktrees `CreatedWorktree`, clones `CreatedClone`; `RemoveWorktree`, `os.RemoveAll` on clones only when `CreatedClone`), then the jobs are deleted from the store.
- `pairing.Pair(ctx, baseURL, name string, id *identity.Identity, out io.Writer) (nodeID, accountID string, err error)`: `POST /api/auth/device/code` form `client_id=wrekin-agent&scope=node` → prints `user_code` and `verification_uri_complete`; polls `POST /api/auth/device/token` (`grant_type=urn:ietf:params:oauth:grant-type:device_code`) honouring `interval` and `slow_down`; on `access_token`: `POST /api/nodes` with `Authorization: Bearer <token>` body `{name, publicKey, agentVersion, platform}` → `{nodeId, accountId}`; then `POST /api/auth/sign-out` with the bearer (best effort). `pairing.Unregister(ctx, baseURL, id)`: `POST /api/nodes/unregister` `{nodeId, timestamp, signature}` with signature over `"wrekin-node-unregister-v1\n"+nodeId+"\n"+timestamp`.
- `service.Manager` interface `{ Install(binary string) error; Start() error; Stop() error; Status() (Status, error); Uninstall() error }` with `service.Detect() Manager` (`systemd` when `systemctl --user` works, `launchd` on darwin, else `none`). Unit `~/.config/systemd/user/wrekin.service` (`ExecStart=<binary> daemon`, `Restart=on-failure`, `WantedBy=default.target`); plist `~/Library/LaunchAgents/io.platyplus.wrekin.plist` (`RunAtLoad`, `KeepAlive`), `launchctl bootstrap gui/<uid>` / `bootout`.
- CLI: `wrekin auth [--name N] [--relay URL]` (generate or reuse identity, pair, save, write settings if missing); `wrekin start [--foreground]`; `wrekin stop`; `wrekin status [--json]`; `wrekin sessions [--json]`; `wrekin attach <session>` (`syscall.Exec` tmux attach); `wrekin logs [-f] [-n 100]`; `wrekin cleanup [--yes] [--dry-run]`; `wrekin logout [--keep-remote]` (unregister then delete identity); hidden `wrekin daemon`; `--version` from `ldflags -X main.version`.

- [ ] **Step 1: Tests** — `TestDaemonHandlerMapsCommands` (runner fakes), `TestControlStatusRoundTrip` (socket in temp dir), `TestPairingFlowAgainstFakeServer` (pending ×2, slow_down ×1, then token; asserts the register body and the bearer header, and the sign-out), `TestUnregisterSignature`, `TestSystemdUnitContent`, `TestLaunchdPlistContent`, `TestCLIStatusWithoutDaemonExplains`.
- [ ] **Steps 2–5**, commit `feat(agent): daemon, control socket, pairing and CLI`.
- [ ] **Step 6: Feasibility notes P2/P3** in `docs/feasibility.md`: run `tmux new -d -s wrekin-probe 'claude --teleport session_00000000000000000000000000'` in a temp git repo, capture the pane after 10 s, record the observed lines (how an invalid id fails, what the prompt looks like), kill the session; record the `.claude/worktrees` convention and the `hasTrustDialogAccepted` key observed in `~/.claude.json`. Commit `docs: feasibility notes for P2 and P3`.

---

# Track B — Cloud (Cloudflare Worker)

### Task B1: Workspace, Wrangler, protocol schemas

**Files:** `cloud/package.json`, `cloud/tsconfig.json`, `cloud/wrangler.jsonc`, `cloud/vitest.config.ts`, `cloud/src/env.ts`, `cloud/src/protocol/{messages.ts,job.ts,errors.ts,index.ts}`, `cloud/test/protocol.test.ts`, `cloud/.dev.vars.example`, `cloud/README.md`

**Interfaces:**
- `wrangler.jsonc`: `name: "wrekin"`, `main: "src/index.ts"`, `compatibility_date` = today, `compatibility_flags: ["nodejs_compat"]`, D1 binding `DB` (database name `wrekin`, `database_id` placeholder `"REPLACE_WITH_D1_ID"`, `migrations_dir: "migrations"`), Durable Object binding `ACCOUNT_HUB` → class `AccountHub` with a `new_sqlite_classes` migration `v1`, vars `BASE_URL: "https://wrekin.platyplus.io"`, routes `[{ pattern: "wrekin.platyplus.io", custom_domain: true }]`, `observability.enabled: true`.
- `env.ts`: `type Env = { DB: D1Database; ACCOUNT_HUB: DurableObjectNamespace<AccountHub>; BASE_URL: string; BETTER_AUTH_SECRET: string; GITHUB_CLIENT_ID; GITHUB_CLIENT_SECRET; GOOGLE_CLIENT_ID; GOOGLE_CLIENT_SECRET }`.
- Protocol schemas (zod): `helloSchema`, `authSchema`, `pingSchema`, `resultSchema`, `nodeMessageSchema` (discriminated on `type`), `commandSchema`, `jobSchema`, `jobStateSchema`, `errorCodes` const array and `ErrorCode` type, `teleportParamsSchema`, `statusParamsSchema`, `approveParamsSchema`, helpers `challenge(nonce)`, `welcome(accountId, nodeId, pingSeconds)`, `command(id, name, params)`, `authMessage(nodeId, nonce): string` returning `"wrekin-node-auth-v1\n"+nodeId+"\n"+nonce`.
- Scripts: `dev`, `deploy`, `typecheck` (`tsc --noEmit`), `test` (`vitest run`), `db:generate` (`bunx @better-auth/cli generate --config src/auth.ts --output migrations/0001_auth.sql`, see B2), `db:migrate:local`, `db:migrate:remote`.

- [ ] **Step 1: Tests** — `protocol.test.ts`: the PROTOCOL.md `hello`, `result` and `Job` examples parse; a `result` with an unknown `error.code` fails; `authMessage` equals the exact string.
- [ ] **Step 2: FAIL. Step 3: Implement** (`bun install` with exact versions: hono ^4.13, better-auth ^1.7.18, @better-auth/mcp ^1.7.18, kysely ^0.29, kysely-d1 ^0.4, @hono/mcp ^0.3, @modelcontextprotocol/sdk ^1.32, zod ^4, wrangler ^4, vitest ^5 — check `@cloudflare/vitest-pool-workers` peer range and pin vitest to it). **Step 4: PASS**, `bun run typecheck` clean. **Step 5: Commit** `feat(cloud): worker workspace and protocol schemas`.

### Task B2: Better Auth on D1

**Files:** `cloud/src/auth.ts`, `cloud/src/db/{kysely.ts,schema.ts}`, `cloud/migrations/0001_auth.sql`, `cloud/migrations/0002_nodes.sql`, `cloud/test/auth.test.ts`

**Interfaces:**
- `createAuth(env: Env)` → Better Auth instance (memoised per `env.DB` identity in a `WeakMap`): `baseURL: env.BASE_URL`, `secret`, `database: { dialect: new D1Dialect({ database: env.DB }), type: "sqlite" }`, `socialProviders: { github, google }`, `account.accountLinking: { enabled: true, trustedProviders: ["github","google"] }`, plugins `[jwt({ disableSettingJwtHeader: true }), bearer(), deviceAuthorization({ verificationUri: "/device", expiresIn: "15m", interval: "5s", userCodeLength: 8, validateClient: id => id === "wrekin-agent" }), mcp({ loginPage: "/login", consentPage: "/consent", resource: env.BASE_URL + "/mcp" })]`, `disabledPaths: ["/token"]`. If `@better-auth/mcp` needs Dynamic Client Registration enabled for Claude's connector, enable it explicitly in the plugin options and say so in a comment (CIMD's Node fetch helper is not available on Workers; a Workers-safe transport is a follow-up).
- `0001_auth.sql`: generated by the Better Auth CLI for this config (user, session, account, verification, jwks, deviceCode, oauth* tables); committed, reviewed, re-generated when the config changes.
- `0002_nodes.sql`: `CREATE TABLE node (id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES user(id) ON DELETE CASCADE, name TEXT NOT NULL, public_key TEXT NOT NULL UNIQUE, agent_version TEXT, platform TEXT, created_at TEXT NOT NULL, last_seen_at TEXT, UNIQUE(account_id, name)); CREATE INDEX node_account ON node(account_id);`
- `db/schema.ts`: Kysely `Database` interface with `node`; `db/kysely.ts`: `getDb(env)`.

- [ ] **Step 1: Tests** (workers pool, D1 with migrations applied in `vitest.config.ts` via `applyD1Migrations`): `GET /.well-known/oauth-protected-resource` returns JSON whose `resource` is `BASE_URL + "/mcp"`; `GET /.well-known/oauth-authorization-server` lists `/oauth2/authorize` and `/oauth2/token` (paths per the plugin); `POST /api/auth/device/code` with `client_id=wrekin-agent` returns `user_code` of 8 chars and `verification_uri` ending in `/device`; with `client_id=other` returns an error.
- [ ] **Steps 2–5**, commit `feat(cloud): Better Auth with GitHub, Google, device authorization and MCP OAuth on D1`.

### Task B3: Node registration API

**Files:** `cloud/src/api/nodes.ts`, `cloud/src/db/nodes.ts`, `cloud/test/nodes.test.ts`

**Interfaces:**
- `nodes.ts` (db): `insertNode({accountId, name, publicKey, agentVersion, platform})` → `node` row with `id = "node_" + <26-char ULID or base32 of 16 random bytes>`; `listNodes(accountId)`; `findNode(accountId, idOrName)`; `findNodeById(id)`; `deleteNode(accountId, id)`; `touchNode(id, lastSeenAt)`.
- Routes (Hono, mounted under `/api/nodes`): `POST /api/nodes` with a Better Auth session (cookie or bearer, via `auth.api.getSession`) → 201 `{nodeId, accountId}`; 409 `{error:"name_taken"}` on the unique name; 400 on a public key that is not 32 base64 bytes. `GET /api/nodes` → the account's nodes with `online` from the hub (B4). `DELETE /api/nodes/:id` (session). `POST /api/nodes/unregister` `{nodeId, timestamp, signature}`: timestamp within ±300 s, Ed25519 verify (WebCrypto `Ed25519`) over `"wrekin-node-unregister-v1\n"+nodeId+"\n"+timestamp` with the stored key → 204.

- [ ] **Step 1: Tests** — register with a test session (create a user + session through the Better Auth API in the test, e.g. `auth.api.signUpEmail` is disabled, so insert rows directly in D1 and set the cookie, or use `auth.api.createSession` if available; pick one and keep a helper `test/helpers/session.ts`), duplicate name → 409, unregister with a good signature → 204 and the node is gone, with a stale timestamp → 401.
- [ ] **Steps 2–5**, commit `feat(cloud): node registration and signed unregistration`.

### Task B4: Account hub Durable Object

**Files:** `cloud/src/hub/account-hub.ts`, `cloud/src/hub/client.ts` (typed wrapper over `stub.fetch`), `cloud/test/hub.test.ts`

**Interfaces:**
- `AccountHub extends DurableObject<Env>`; `fetch(request)` routes: `GET /connect?nodeId=…` (Upgrade; headers `X-Wrekin-Public-Key`, set by the Worker from D1): creates a `WebSocketPair`, `acceptWebSocket(server, [nodeId])`, stores an attachment `{nodeId, publicKey, stage:"hello"|"authed", nonce?, connectedAt, agentVersion?, harnesses?}`, sends nothing until `hello`. `webSocketMessage`: `hello` → check `publicKey` equals the header's, send `challenge` (nonce = 32 random bytes base64), stage `challenged`; `auth` → verify via WebCrypto Ed25519 over `authMessage(nodeId, nonce)` → `welcome` (pingSeconds 30), close any *other* socket of the same `nodeId` with `4409`, `touchNode`; a `result` → resolves the pending promise of `id`; malformed → close `4400`; auth failure → `4401`. `setWebSocketAutoResponse(new WebSocketRequestResponsePair('{"v":1,"type":"ping"}','{"v":1,"type":"pong"}'))` in the constructor. `webSocketClose/Error`: nothing but logging (the attachment goes with the socket).
- Internal routes: `GET /nodes/online` → `{ nodes: [{ nodeId, connectedAt, agentVersion, harnesses }] }` from `getWebSockets()` attachments with stage `authed`; `POST /command` `{ nodeId, name, params, timeoutMs = 30000 }` → `{ ok, data } | { ok:false, error:{code,message} }` with `node_offline` when no authed socket for `nodeId`, `node_timeout` on timeout. Pending commands in an in-memory `Map<id, {resolve, timer}>`.
- `hub/client.ts`: `hubFor(env, accountId)` → `{ online(): Promise<OnlineNode[]>; command<T>(nodeId, name, params, timeoutMs?): Promise<Result<T>> }`.

- [ ] **Step 1: Tests** (workers pool with `runInDurableObject` or through the Worker's `/connect` using a WebSocket client in the test): handshake with a key pair generated in the test (`crypto.subtle.generateKey("Ed25519")`) → `welcome`; wrong signature → close 4401; `command` round-trip (test client answers `result`); `command` for a node that is not connected → `node_offline` immediately; `command` not answered → `node_timeout` with `timeoutMs: 100`; second socket for the same node → first closed with 4409; `ping` text gets `pong`.
- [ ] **Steps 2–5**, commit `feat(cloud): per-account Durable Object routing node commands`.

### Task B5: Worker routes — connect, pages

**Files:** `cloud/src/index.ts`, `cloud/src/connect.ts`, `cloud/src/pages/{layout.ts,home.ts,login.ts,device.ts,consent.ts,nodes.ts}`, `cloud/test/connect.test.ts`, `cloud/test/pages.test.ts`

**Interfaces:**
- `GET /connect?node=<id>`: 426 unless `Upgrade: websocket`; D1 lookup `findNodeById` → 404 `unknown node` (plain, the agent then waits the cap); forward to `hubFor(env, node.account_id)` stub with the request plus `X-Wrekin-Public-Key`.
- `app.all("/api/auth/*", c => createAuth(c.env).handler(c.req.raw))`, before every other route.
- Pages (server-rendered with `hono/html`, one shared layout, no framework, inline CSS, dark-mode aware, mobile-first since the device page is opened from a phone): `/` what Wrekin is + install snippet (`brew install plmercereau/tap/wrekin`, `nix run github:plmercereau/wrekin`, GitHub releases) + "connect Claude: add `https://wrekin.platyplus.io/mcp` as a remote MCP server"; `/login` (two buttons that `POST /api/auth/sign-in/social` with `provider` and `callbackURL` = `redirect` query, default `/nodes`); `/device` (`user_code` prefilled from the query; unauthenticated → `/login?redirect=…`; shows the code and client "Wrekin agent", Approve/Deny buttons calling `POST /api/auth/device/approve|deny` then a confirmation state that tells the user to return to the terminal); `/consent` (the MCP OAuth consent page the plugin redirects to: shows client name and scopes, Approve/Deny posting to the plugin's consent endpoint per its docs); `/nodes` (list with name, fingerprint = first 16 hex of SHA-256 of the raw key grouped by 4, online badge, last seen, Remove button → `DELETE /api/nodes/:id`).
- 404 and error handlers return JSON for `/api/*` and `/mcp`, HTML otherwise.

- [ ] **Step 1: Tests** — `/connect` without upgrade → 426; unknown node → 404; `/` 200 contains "wrekin.platyplus.io/mcp"; `/device?user_code=ABCD-EFGH` unauthenticated → 302 to `/login?redirect=…`; `/nodes` unauthenticated → 302.
- [ ] **Steps 2–5**, commit `feat(cloud): connect route and pages`.

### Task B6: MCP server and tools

**Files:** `cloud/src/mcp/{server.ts,tools.ts,session-id.ts}`, `cloud/src/mcp/route.ts`, `cloud/test/mcp.test.ts`

**Interfaces:**
- `parseCloudSessionId(input: string): string | null` accepts `session_…` ids and `https://claude.ai/code/session_…` URLs (returns the id), else null.
- `buildMcpServer(ctx: { env: Env; accountId: string })` → `McpServer` with tools:
  - `list_nodes` (no input) → `{ nodes: [{ id, name, online, lastSeenAt, agentVersion, harnesses }] }`.
  - `teleport` input `{ node: string /* id or name */, session: string /* id or claude.ai/code URL */, repository?: string, directory?: string, relaunch?: boolean }` → resolves node (`unknown_node`), parses session (`invalid_params` with a message telling Claude to pass the claude.ai/code URL), hub `command("teleport", {harness:"claude-code", cloudSessionId, repository, directory, relaunch})` → the `Job`.
  - `get_teleport_status` input `{ node, jobId }` → `Job`.
  - `approve_repository` input `{ node, jobId, approved: boolean }` → `Job`.
  - Every tool returns `structuredContent` (the object) plus a one-paragraph `text` summary; errors return `isError: true` with `{ code, message }` in both.
  - Tool descriptions tell Claude: it must pass its own Claude Code Cloud session id or URL (not a chat URL); `needs_attention` means asking the user and calling `approve_repository`; `ready.confirmed === false` means the session is running but unverified and the user should run the `session.attach` command.
- `route.ts`: `app.post("/mcp", …)` wrapped by `requireMcpAuth(auth, handler, { resource })` from `@better-auth/mcp`, with `accountId = claims.sub`; per request a fresh `McpServer` + `StreamableHTTPTransport` from `@hono/mcp` (stateless). `GET`/`DELETE /mcp` → 405.

- [ ] **Step 1: Tests** — `parseCloudSessionId` table; tools unit-tested with a fake hub (`online()`/`command()` stubs): `list_nodes` merges D1 rows with online state; `teleport` with a chat URL → `invalid_params`; `teleport` to a node of another account → `unknown_node` (Review Focus 5); `teleport` to an offline node → `node_offline`; unauthenticated `POST /mcp` → 401 with `WWW-Authenticate`.
- [ ] **Steps 2–5**, commit `feat(cloud): MCP server with list_nodes, teleport, get_teleport_status, approve_repository`.

---

# Track C — Distribution and CI

### Task C1: GitHub Actions CI

**Files:** `.github/workflows/ci.yml`

- Jobs: `go` (matrix ubuntu-latest, macos-latest; `actions/setup-go` with `go.mod`; `sudo apt-get install -y tmux` / `brew install tmux`; `gofmt -l` must be empty; `go vet ./...`; `go test -race ./...`), `cloud` (`oven-sh/setup-bun`; `bun install --frozen-lockfile`; `bun run typecheck`; `bun run test`, working dir `cloud`), `goreleaser-check` (`goreleaser/goreleaser-action` with `args: check`), `nix` (`DeterminateSystems/nix-installer-action`; `nix flake check`; `nix build .#wrekin`).
- Triggers: `pull_request`, `push` to `main`. Concurrency per ref.
- [ ] Commit `ci: go, cloud, goreleaser and nix checks`.

### Task C2: GoReleaser and release workflow

**Files:** `.goreleaser.yaml`, `.github/workflows/release.yml`, `packaging/README.md`

- `builds`: `main: ./cmd/wrekin`, `binary: wrekin`, `env: CGO_ENABLED=0`, goos linux/darwin, goarch amd64/arm64, `ldflags: -s -w -X main.version={{.Version}}`; `archives` tar.gz with `README.md`, `LICENSE`; `checksum`; `changelog` grouped by conventional-commit prefix; `brews` (v2 schema: `homebrew_casks` is for casks; use `brews`): `repository: { owner: plmercereau, name: homebrew-tap, token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}" }`, `homepage: https://wrekin.platyplus.io`, `description`, `service` block running `wrekin daemon` with `keep_alive true` and a log path under `var/log`, `test` running `wrekin --version`, `skip_upload: auto`.
- `release.yml`: on `push` tags `v*`: checkout with history, setup-go, `goreleaser/goreleaser-action` `release --clean` with `GITHUB_TOKEN` and `HOMEBREW_TAP_GITHUB_TOKEN`.
- `packaging/README.md`: how a release is cut, the tap repository that must exist, the secret.
- [ ] Verify locally: `goreleaser check` (via `nix shell nixpkgs#goreleaser` or `go run github.com/goreleaser/goreleaser/v2@latest check`); commit `build: goreleaser and release workflow`.

### Task C3: Nix flake and Home Manager module

**Files:** `flake.nix`, `nix/module.nix`, `nix/package.nix`, `.envrc`? (no), `flake.lock`

- `packages.<system>.wrekin` (`buildGoModule`, `vendorHash` computed with `nix build` — if the sandbox blocks it, set `lib.fakeHash`, run once, paste the hash, and leave a comment on how to refresh), `packages.default = wrekin`, `apps.default`, `devShells.default` (go, bun, wrangler via `nodePackages.wrangler` or `bun x`, goreleaser, tmux), `homeManagerModules.default = import ./nix/module.nix`, `homeManagerModules.wrekin`.
- Module `services.wrekin`: `enable`, `package`, `settings` (attrset → `xdg.configFile."wrekin/settings.json"`), Linux: `systemd.user.services.wrekin` (`ExecStart = "${package}/bin/wrekin daemon"`, `Restart = "on-failure"`, `WantedBy = ["default.target"]`), darwin: `launchd.agents.wrekin` (`ProgramArguments`, `RunAtLoad`, `KeepAlive`).
- [ ] Verify: `nix flake check` and `nix build` (or document the failure if the sandbox forbids network); commit `build: nix flake with package, app and Home Manager module`.

### Task C4: Repository docs

**Files:** `README.md`, `LICENSE` (MIT, copyright Pierre-Louis Mercereau 2026), `docs/feasibility.md` (P1–P4 status; P1 and P4 have procedures and what remains to verify on Claude Mobile/Web and on the deployed Worker), `CONTRIBUTING.md` (how to run both halves locally: `wrangler dev` + `wrekin auth --relay ws://localhost:8787/connect`), `.gitignore`, `.editorconfig`.

- README sections: what it is (two paragraphs), how it works (the journey of spec §5 as a numbered list), install (brew, nix, releases), `wrekin auth` → `wrekin start` → connect Claude to the MCP URL → ask Claude to teleport, the CLI table, configuration (the spec's example file), security model (spec §4 in short, with the accepted trade-off stated), status (MVP; the four feasibility items), layout, licence.
- [ ] Commit `docs: README, licence, contributing, feasibility`.

---

# Track D — platyplus/infrastructure PR

### Task D1: Site stack, docs, CI matrix

**Files (in `~/dev/platyplus/infrastructure`, branch `claude/wrekin-site`):**
- Create: `sites/wrekin/tofu/main.tf`, `sites/wrekin/tofu/variables.tf`, `sites/wrekin/tofu/terraform.tfvars`, `sites/wrekin/README.md`, `docs/specs/2026-11-05-wrekin-site-design.md`, `docs/runbooks/2026-11-wrekin-site.md`
- Modify: `README.md` (tenant table row + Layout: `sites/wrekin` is a tenant with nothing in the cluster), `.github/workflows/iac-tofu.yml` (matrix `dir` += `sites/wrekin/tofu`)

**Stack (`main.tf`):** same `terraform` block as `sites/edgehub/tofu` with key `wrekin/terraform.tfstate`; providers: cloudflare only (no Scaleway: nothing of Wrekin's is in the cluster); `data "cloudflare_zone" "platyplus" { zone_id = var.cloudflare_zone_id }` for the account id; `resource "cloudflare_d1_database" "wrekin" { account_id = data.cloudflare_zone.platyplus.account.id, name = "wrekin", primary_location_hint = "weur", lifecycle { prevent_destroy = true } }`; `resource "cloudflare_workers_custom_domain" "wrekin" { account_id, hostname = "wrekin.platyplus.io", service = "wrekin", zone_id = var.cloudflare_zone_id }`; outputs `d1_database_id`, `hostname`. Comments explain: the Worker's code and secrets are deployed by the wrekin repository (`wrangler deploy`), this stack owns what outlives a deploy (the database, the name); the custom domain needs the script to exist, hence the two-step first apply in the runbook; the zone's `ssl` setting is irrelevant (Workers custom domains are Cloudflare's own origin).

**Runbook:** 1) OAuth apps: GitHub OAuth App (callback `https://wrekin.platyplus.io/api/auth/callback/github`), Google OAuth client (redirect `https://wrekin.platyplus.io/api/auth/callback/google`). 2) Token: `CLOUDFLARE_API_TOKEN` needs, beyond the zone permissions, account-level `D1:Edit` and `Workers Scripts:Edit`; say so because today's token has only zone settings. 3) `tofu -chdir=sites/wrekin/tofu init && plan && apply -target=cloudflare_d1_database.wrekin`, read the output id. 4) In the wrekin repo: put the id in `cloud/wrangler.jsonc`, `wrangler secret put` × 5, `wrangler d1 migrations apply wrekin --remote`, `wrangler deploy`. 5) Full `apply` (the domain); `curl -I https://wrekin.platyplus.io/` → 200. 6) First pairing: `wrekin auth`, approve on `/device`, `wrekin start`, `wrekin status` shows connected; add `https://wrekin.platyplus.io/mcp` in Claude's connectors; `list_nodes` shows the node. 7) Rollback: `wrangler rollback`; the database is kept (`prevent_destroy`); removing the domain is `tofu destroy -target`. Each step ends with "Go on when: …" like the existing runbooks.

- [ ] Validate: `nix shell nixpkgs#opentofu -c sh -c 'cd sites/wrekin/tofu && tofu fmt -check -recursive && tofu init -backend=false && tofu validate'`; commit in the infra repo's voice (see its git log: a sentence, not a conventional prefix), e.g. `The Wrekin site: its D1 database and its name on the Worker, nothing in the cluster`.

---

## Integration (after the tracks)

1. Merge the track branches into `feat/mvp` (conflicts only in `README.md`/`docs`), run `go test ./...`, `cd cloud && bun run test && bun run typecheck`, `goreleaser check`, `nix flake check`.
2. End-to-end on this machine without Cloudflare: `cd cloud && bunx wrangler dev` (D1 + DO locally, `.dev.vars` with dummy OAuth secrets), create a user row and a node row in the local D1, write `identity.json` for that node (test helper `go run ./internal/devtools/pairlocal` or a documented `sqlite3` line), `wrekin start --foreground --settings <tmp>`, confirm `connected` in the Worker logs, then call the `teleport` command through the hub (a `wrangler dev` debug route `POST /debug/command` enabled only when `env.DEBUG === "1"`) with a `file://` repository and a fake harness? No: the real harness. Use `directory` pointing at a scratch clone of this repository and `cloudSessionId` of this very session: the job must reach `launching` and then `ready` or `harness_exited`; record the outcome in `docs/feasibility.md` (P2).
3. Push the branch, open the PR with the summary, the test evidence and the P1–P4 table.
