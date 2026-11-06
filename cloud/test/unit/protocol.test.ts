import { describe, expect, it } from "vitest";
import {
  authMessage,
  challenge,
  command,
  commandNames,
  commandParamsSchemas,
  commandSchema,
  errorCodes,
  helloSchema,
  jobSchema,
  nodeMessageSchema,
  resultSchema,
  teleportParamsSchema,
  welcome,
} from "../../src/protocol";

// The examples below are copied from protocol/PROTOCOL.md.
const helloExample = {
  v: 1,
  type: "hello",
  nodeId: "node_01J",
  publicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
  name: "workstation",
  agentVersion: "0.1.0",
  platform: { os: "darwin", arch: "arm64", hostname: "mbp" },
  harnesses: [{ id: "claude-code", available: true, version: "2.1.318" }],
};

const jobExample = {
  id: "job_01J",
  harness: "claude-code",
  cloudSessionId: "session_01CN",
  state: "ready",
  repository: {
    input: "plmercereau/wrekin",
    url: "https://github.com/plmercereau/wrekin",
    name: "wrekin",
  },
  workdir: "/home/me/projects/wrekin/.claude/worktrees/wrekin-01CN",
  session: { terminal: "tmux", name: "wrekin-01CN", attach: "wrekin attach wrekin-01CN" },
  attention: null,
  ready: { confirmed: true, detail: "prompt visible" },
  error: null,
  createdAt: "2026-11-05T22:10:03Z",
  updatedAt: "2026-11-05T22:10:41Z",
};

describe("node messages", () => {
  it("parses the hello example", () => {
    const parsed = helloSchema.parse(helloExample);
    expect(parsed.nodeId).toBe("node_01J");
    expect(parsed.harnesses[0]?.id).toBe("claude-code");
  });

  it("ignores unknown fields, as the protocol requires", () => {
    const parsed = helloSchema.parse({ ...helloExample, future: "field" });
    expect("future" in parsed).toBe(false);
  });

  it("parses a successful result", () => {
    const parsed = resultSchema.parse({ v: 1, type: "result", id: "cmd_1", ok: true, data: { a: 1 } });
    expect(parsed.ok).toBe(true);
    if (parsed.ok) expect(parsed.data).toEqual({ a: 1 });
  });

  it("parses a failed result with a known error code", () => {
    const parsed = resultSchema.parse({
      v: 1,
      type: "result",
      id: "cmd_1",
      ok: false,
      error: { code: "unknown_job", message: "no such job" },
    });
    expect(parsed.ok).toBe(false);
    if (!parsed.ok) expect(parsed.error.code).toBe("unknown_job");
  });

  it("rejects a result with an unknown error code", () => {
    const outcome = resultSchema.safeParse({
      v: 1,
      type: "result",
      id: "cmd_1",
      ok: false,
      error: { code: "made_up", message: "nope" },
    });
    expect(outcome.success).toBe(false);
  });

  it("rejects a frame of another protocol version", () => {
    expect(helloSchema.safeParse({ ...helloExample, v: 2 }).success).toBe(false);
  });

  it("discriminates node messages on type", () => {
    expect(nodeMessageSchema.parse({ v: 1, type: "ping" }).type).toBe("ping");
    expect(nodeMessageSchema.parse({ v: 1, type: "auth", signature: "c2ln" }).type).toBe("auth");
    expect(nodeMessageSchema.parse(helloExample).type).toBe("hello");
    expect(nodeMessageSchema.safeParse({ v: 1, type: "nonsense" }).success).toBe(false);
  });
});

describe("relay messages", () => {
  it("builds challenge, welcome and command frames", () => {
    expect(challenge("bm9uY2U=")).toEqual({ v: 1, type: "challenge", nonce: "bm9uY2U=" });
    expect(welcome("usr_1", "node_1", 30)).toEqual({
      v: 1,
      type: "welcome",
      accountId: "usr_1",
      nodeId: "node_1",
      pingSeconds: 30,
    });
    const frame = command("cmd_1", "teleport", { harness: "claude-code", cloudSessionId: "session_1" });
    expect(commandSchema.parse(frame)).toEqual(frame);
  });

  it("signs the exact handshake string", () => {
    expect(authMessage("node_01J", "bm9uY2U=")).toBe("wrekin-node-auth-v1\nnode_01J\nbm9uY2U=");
  });
});

describe("jobs and params", () => {
  it("parses the Job example", () => {
    const job = jobSchema.parse(jobExample);
    expect(job).toEqual(jobExample);
  });

  it("rejects an unknown job state", () => {
    expect(jobSchema.safeParse({ ...jobExample, state: "done" }).success).toBe(false);
  });

  it("validates teleport params", () => {
    expect(teleportParamsSchema.safeParse({ harness: "claude-code" }).success).toBe(false);
    expect(
      teleportParamsSchema.parse({ harness: "claude-code", cloudSessionId: "session_1", relaunch: true }),
    ).toEqual({ harness: "claude-code", cloudSessionId: "session_1", relaunch: true });
  });

  it("has a parameter schema for every command, empty for list_jobs and ping", () => {
    expect(Object.keys(commandParamsSchemas).sort()).toEqual([...commandNames].sort());
    expect(commandParamsSchemas.list_jobs.safeParse({}).success).toBe(true);
    expect(commandParamsSchemas.ping.safeParse({ extra: 1 }).success).toBe(true);
    expect(commandParamsSchemas.get_teleport_status.safeParse({}).success).toBe(false);
    expect(commandParamsSchemas.approve_repository.safeParse({ jobId: "job_1", approved: true }).success).toBe(true);
    expect(commandParamsSchemas.teleport.safeParse({ harness: 1 }).success).toBe(false);
  });

  it("lists every error code of the protocol table", () => {
    expect(errorCodes).toContain("node_offline");
    expect(errorCodes).toContain("session_gone");
    expect(errorCodes).toContain("internal");
    expect(errorCodes).toHaveLength(19);
  });
});
