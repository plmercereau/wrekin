# Wrekin node protocol, version 1

The contract between a node (the Go agent) and the relay (the Cloudflare
Worker and its per-account Durable Object). It is independent of Claude Code:
the harness is a string, and a node advertises the harnesses it can run.

Both sides implement this document. The Go types live in
`internal/protocol`, the TypeScript schemas in `cloud/src/protocol`. A
change to the wire format bumps `v` and is recorded in the changelog at the
end of this file.

## Transport

- One WebSocket per node, opened by the node to
  `GET <relay.url>?node=<nodeId>` (default `wss://wrekin.platyplus.io/connect`).
- Text frames only, one JSON object per frame, UTF-8.
- Every object carries `"v": 1` and a `"type"`.
- Unknown `type` values are ignored by both sides (forward compatibility
  within a version); unknown fields are ignored.
- The relay closes with a close code from the table below on a fatal
  condition; the node reconnects with exponential backoff (initial 1 s,
  factor 2, cap 60 s, full jitter) except after `4404` and `4401`, where it
  waits the cap before retrying, since the node is unknown or its key is
  rejected and only an operator can fix that.

| Close code | Meaning |
|---|---|
| `4400` | Protocol error (malformed frame, wrong order) |
| `4401` | Authentication failed (bad signature) |
| `4404` | Unknown node id |
| `4409` | Replaced by a newer connection of the same node |
| `4429` | Too many connection attempts |

## Handshake

The node must authenticate within 10 seconds of opening the socket.

1. Node → relay `hello`

```json
{
  "v": 1,
  "type": "hello",
  "nodeId": "node_01J…",
  "publicKey": "<base64, 32 raw Ed25519 bytes>",
  "name": "workstation",
  "agentVersion": "0.1.0",
  "platform": { "os": "darwin", "arch": "arm64", "hostname": "mbp" },
  "harnesses": [
    { "id": "claude-code", "available": true, "version": "2.1.318" }
  ]
}
```

2. Relay → node `challenge`

```json
{ "v": 1, "type": "challenge", "nonce": "<base64, 32 random bytes>" }
```

3. Node → relay `auth`

```json
{ "v": 1, "type": "auth", "signature": "<base64, 64 bytes>" }
```

The signature is Ed25519 over the UTF-8 bytes of

```
"wrekin-node-auth-v1\n" + nodeId + "\n" + nonce
```

where `nonce` is the base64 string exactly as received.

4. Relay → node `welcome`, or `error` followed by a close.

```json
{ "v": 1, "type": "welcome", "accountId": "usr_…", "nodeId": "node_01J…", "pingSeconds": 30 }
```

The relay verifies the signature against the public key registered for
`nodeId` at pairing time; the `publicKey` in `hello` is informational and
must equal the registered one or the relay closes with `4401`.

## Liveness

- The node sends `{"v":1,"type":"ping"}` every `pingSeconds`; the relay
  answers `{"v":1,"type":"pong"}` (served by the Durable Object's
  auto-response, so it does not wake the object).
- A node that gets no `pong` within 10 seconds closes and reconnects.
- The relay treats a node as offline the moment its socket closes.

## Commands

The relay sends commands; the node answers each one exactly once.

```json
{ "v": 1, "type": "command", "id": "cmd_…", "name": "teleport", "params": { … } }
```

```json
{ "v": 1, "type": "result", "id": "cmd_…", "ok": true, "data": { … } }
{ "v": 1, "type": "result", "id": "cmd_…", "ok": false, "error": { "code": "unknown_job", "message": "…" } }
```

The node de-duplicates on `id`: a command id seen in the last 10 minutes is
answered with the cached result and not executed again. The relay waits at
most 30 seconds for a result and otherwise answers its caller with
`node_timeout`. A command for a node with no open socket fails at once
with `node_offline`; nothing is queued.

### `teleport`

Params:

| Field | Type | Notes |
|---|---|---|
| `harness` | string | `"claude-code"` is the only MVP value |
| `cloudSessionId` | string | the Claude Code Cloud session id, e.g. `session_01CN…` |
| `repository` | string, optional | a Git URL or `owner/name` (GitHub); used to find or clone the checkout |
| `directory` | string, optional | a path hint under an allowed root; `~` is expanded |
| `relaunch` | boolean, optional | required to launch again when the previous session is gone |

Result: a `Job`. Returns within a second: the job is `accepted` (or the
existing job of the same `(harness, cloudSessionId)`), and the work
continues asynchronously.

### `get_teleport_status`

Params: `{ "jobId": "job_…" }`. Result: a `Job`.

### `approve_repository`

Params: `{ "jobId": "job_…", "approved": true }`. Result: the `Job` after
the decision (`preparing` when approved, `failed` with `not_approved`
otherwise). Only valid on a job in `needs_attention`; otherwise
`attention_not_pending`.

### `list_jobs`

Params: `{}`. Result: `{ "jobs": [Job, …] }`, newest first.

### `ping`

Params: `{}`. Result: `{ "at": "<RFC 3339>" }`. For diagnostics only.

## Job

```json
{
  "id": "job_01J…",
  "harness": "claude-code",
  "cloudSessionId": "session_01CN…",
  "state": "ready",
  "repository": { "input": "plmercereau/wrekin", "url": "https://github.com/plmercereau/wrekin", "name": "wrekin" },
  "workdir": "/home/me/projects/wrekin/.claude/worktrees/wrekin-01CN",
  "session": { "terminal": "tmux", "name": "wrekin-01CN", "attach": "wrekin attach wrekin-01CN" },
  "attention": null,
  "ready": { "confirmed": true, "detail": "prompt visible" },
  "error": null,
  "createdAt": "2026-11-05T22:10:03Z",
  "updatedAt": "2026-11-05T22:10:41Z"
}
```

- `state` ∈ `accepted | preparing | needs_attention | launching | ready | failed`.
- `repository`, `workdir`, `session`, `attention`, `ready`, `error` are
  `null` when not applicable.
- `attention` is present only in `needs_attention`:
  `{ "kind": "approve_repository", "message": "…", "repositoryUrl": "…", "proposedPath": "…" }`.
- `ready.confirmed` is `false` when the harness is still running after the
  readiness timeout but the prompt was never seen: the job is usable but
  unverified (the spec's "uncertain outcome"), and `detail` says why.
- Nothing in a `Job` is raw terminal output; `detail` and `message` are
  sentences the node writes.

## Error codes

Stable strings, shared by the node, the relay and the MCP tools.

| Code | Raised by | Meaning |
|---|---|---|
| `node_offline` | relay | the node has no open connection |
| `node_timeout` | relay | the node did not answer within 30 s |
| `unknown_node` | relay | no node of that id or name on this account |
| `invalid_params` | both | a parameter is missing or malformed |
| `unknown_job` | node | no job of that id |
| `repository_required` | node | neither `repository` nor `directory` was given |
| `directory_not_allowed` | node | the directory is outside every allowed root |
| `path_escapes_root` | node | the canonical path (after symlinks) leaves its root |
| `harness_unavailable` | node | the harness binary was not found |
| `terminal_unavailable` | node | tmux was not found |
| `concurrency_limit` | node | `teleport.maxConcurrent` preparations are already running |
| `attention_not_pending` | node | `approve_repository` on a job that is not waiting |
| `not_approved` | node | the user declined the repository |
| `clone_failed` | node | `git clone` failed (the reason stays in the local log) |
| `git_prompt_blocked` | node | Git asked for credentials interactively; refused |
| `worktree_failed` | node | the isolated worktree could not be created |
| `harness_exited` | node | the harness process exited before the session was ready |
| `session_gone` | node | the tmux session of an existing job has disappeared; retry with `relaunch` |
| `internal` | both | anything else; the message is generic |

## Changelog

- v1 (2026-11-05): initial version.
