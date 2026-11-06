import { Client } from "@modelcontextprotocol/client";
import { InMemoryTransport } from "@modelcontextprotocol/server";
import { beforeAll, describe, expect, it } from "vitest";
import { buildMcpServer } from "../../src/mcp/server";
import { parseCloudSessionId } from "../../src/mcp/session-id";
import type { Job } from "../../src/protocol";
import { FakeHub } from "../helpers/fake-hub";
import { generateNodeKey } from "../helpers/keys";
import { MemoryNodeStore } from "../helpers/memory-nodes";
import { nodeWorker } from "../helpers/node-app";

describe("parseCloudSessionId", () => {
  it.each([
    ["session_01CNrG8QvmZWiHRpzs2xhqhN", "session_01CNrG8QvmZWiHRpzs2xhqhN"],
    ["  session_01CNrG8QvmZWiHRpzs2xhqhN\n", "session_01CNrG8QvmZWiHRpzs2xhqhN"],
    ["https://claude.ai/code/session_01CNrG8QvmZWiHRpzs2xhqhN", "session_01CNrG8QvmZWiHRpzs2xhqhN"],
    ["https://claude.ai/code/session_01CNrG8QvmZWiHRpzs2xhqhN?foo=bar", "session_01CNrG8QvmZWiHRpzs2xhqhN"],
    ["claude.ai/code/session_01CNrG8QvmZWiHRpzs2xhqhN", "session_01CNrG8QvmZWiHRpzs2xhqhN"],
    ["https://claude.ai/chat/0b7f8c2e-1111-2222-3333-444455556666", null],
    ["https://claude.ai/code", null],
    ["0b7f8c2e-1111-2222-3333-444455556666", null],
    ["session_", null],
    ["", null],
  ])("%s → %s", (input, expected) => {
    expect(parseCloudSessionId(input)).toBe(expected);
  });

  it("bounds the id at 190 characters after the prefix, as the daemon does", () => {
    const longest = `session_${"a".repeat(190)}`;
    expect(parseCloudSessionId(longest)).toBe(longest);
    expect(parseCloudSessionId(`https://claude.ai/code/${longest}`)).toBe(longest);
    expect(parseCloudSessionId(`session_${"a".repeat(191)}`)).toBeNull();
    expect(parseCloudSessionId(`session_${"a".repeat(10_000)}`)).toBeNull();
    expect(parseCloudSessionId(`https://claude.ai/code/session_${"a".repeat(10_000)}`)).toBeNull();
  });
});

const readyJob = (over: Partial<Job> = {}): Job => ({
  id: "job_01J",
  harness: "claude-code",
  cloudSessionId: "session_01CNrG8QvmZWiHRpzs2xhqhN",
  state: "ready",
  repository: { input: "plmercereau/wrekin", url: "https://github.com/plmercereau/wrekin", name: "wrekin" },
  workdir: "/home/me/projects/wrekin",
  session: { terminal: "tmux", name: "wrekin-01CN", attach: "wrekin attach wrekin-01CN" },
  attention: null,
  ready: { confirmed: true, detail: "prompt visible" },
  error: null,
  createdAt: "2026-11-05T22:10:03Z",
  updatedAt: "2026-11-05T22:10:41Z",
  ...over,
});

describe("MCP tools", () => {
  const hub = new FakeHub();
  const nodes = new MemoryNodeStore();
  let client: Client;
  let mine: { id: string };
  let offline: { id: string };
  let theirs: { id: string };

  type ToolResult = { isError?: boolean; structuredContent?: Record<string, unknown>; content: Array<{ type: string; text?: string }> };
  const call = async (name: string, args: Record<string, unknown> = {}) => (await client.callTool({ name, arguments: args })) as ToolResult;
  const textOf = (r: ToolResult) => r.content.map((c) => c.text ?? "").join("\n");

  beforeAll(async () => {
    mine = await nodes.insertNode({ accountId: "acc_me", name: "workstation", publicKey: (await generateNodeKey()).publicKey, agentVersion: "0.1.0" });
    offline = await nodes.insertNode({ accountId: "acc_me", name: "laptop", publicKey: (await generateNodeKey()).publicKey });
    await nodes.touchNode(offline.id, "2026-11-01T10:00:00.000Z");
    theirs = await nodes.insertNode({ accountId: "acc_other", name: "workstation-of-someone-else", publicKey: (await generateNodeKey()).publicKey });
    hub.setOnline("acc_me", [{ nodeId: mine.id, connectedAt: "2026-11-06T00:00:00Z", agentVersion: "0.1.0", harnesses: [{ id: "claude-code", available: true, version: "2.1.318" }] }]);
    hub.setOnline("acc_other", [{ nodeId: theirs.id, connectedAt: "2026-11-06T00:00:00Z" }]);

    const server = buildMcpServer({ accountId: "acc_me", nodes, hub: hub.factory("acc_me") });
    const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
    await server.connect(serverTransport);
    client = new Client({ name: "test", version: "0.0.0" });
    await client.connect(clientTransport);
  });

  it("exposes the four tools", async () => {
    const { tools } = await client.listTools();
    expect(tools.map((t) => t.name).sort()).toEqual(["approve_repository", "get_teleport_status", "list_nodes", "teleport"]);
    const teleport = tools.find((t) => t.name === "teleport");
    expect(teleport?.description).toContain("https://claude.ai/code/session_");
    expect(teleport?.description).toContain("approve_repository");
    expect(teleport?.description).toContain("ready.confirmed");
  });

  it("list_nodes merges the stored nodes with the online state, scoped to the account", async () => {
    const result = await call("list_nodes");
    expect(result.isError).toBeFalsy();
    const { nodes: listed } = result.structuredContent as { nodes: Array<Record<string, unknown>> };
    expect(listed.map((n) => n.name).sort()).toEqual(["laptop", "workstation"]);
    expect(listed.find((n) => n.name === "workstation")).toMatchObject({ id: mine.id, online: true, agentVersion: "0.1.0", harnesses: [{ id: "claude-code", available: true, version: "2.1.318" }] });
    expect(listed.find((n) => n.name === "laptop")).toMatchObject({ online: false, lastSeenAt: "2026-11-01T10:00:00.000Z" });
    expect(textOf(result)).toContain("workstation (online, claude-code 2.1.318)");
    expect(textOf(result)).toContain("laptop (offline, last seen 2026-11-01 10:00:00Z)");
  });

  it("teleport refuses a chat URL with invalid_params and a hint", async () => {
    const result = await call("teleport", { node: "workstation", session: "https://claude.ai/chat/0b7f8c2e-1111-2222-3333-444455556666" });
    expect(result.isError).toBe(true);
    expect(result.structuredContent).toEqual({ error: { code: "invalid_params", message: expect.stringContaining("https://claude.ai/code/session_") } });
    expect(hub.commands).toHaveLength(0);
  });

  it("bounds every string argument before anything reaches the hub", async () => {
    const session = "session_01CNrG8QvmZWiHRpzs2xhqhN";
    const before = hub.commands.length;
    const refused = async (name: string, args: Record<string, unknown>, field: string) => {
      const result = await call(name, args);
      expect(result.isError, field).toBe(true);
      expect(textOf(result), field).toContain(field);
    };
    await refused("teleport", { node: "x".repeat(257), session }, "node");
    await refused("teleport", { node: "workstation", session: `${session}${"x".repeat(2048)}` }, "session");
    await refused("teleport", { node: "workstation", session, repository: "r".repeat(2049) }, "repository");
    await refused("teleport", { node: "workstation", session, directory: "d".repeat(2049) }, "directory");
    await refused("get_teleport_status", { node: "workstation", jobId: "j".repeat(257) }, "jobId");
    expect(hub.commands).toHaveLength(before);
  });

  it("teleport to a node of another account answers unknown_node, never node_offline (Review Focus 5)", async () => {
    for (const ref of [theirs.id, "workstation-of-someone-else"]) {
      const result = await call("teleport", { node: ref, session: "session_01CNrG8QvmZWiHRpzs2xhqhN" });
      expect(result.isError).toBe(true);
      expect((result.structuredContent as { error: { code: string } }).error.code).toBe("unknown_node");
    }
    expect(hub.commands).toHaveLength(0);
  });

  it("teleport to an offline node answers node_offline", async () => {
    const result = await call("teleport", { node: "laptop", session: "session_01CNrG8QvmZWiHRpzs2xhqhN" });
    expect(result.isError).toBe(true);
    expect((result.structuredContent as { error: { code: string } }).error.code).toBe("node_offline");
  });

  it("teleport sends the command with the parsed session id and returns the Job", async () => {
    hub.answer(mine.id, "teleport", { ok: true, data: readyJob() });
    const result = await call("teleport", {
      node: "workstation",
      session: "https://claude.ai/code/session_01CNrG8QvmZWiHRpzs2xhqhN",
      repository: "plmercereau/wrekin",
    });
    expect(result.isError).toBeFalsy();
    const sent = hub.commands[hub.commands.length - 1];
    expect(sent).toMatchObject({
      nodeId: mine.id,
      name: "teleport",
      params: { harness: "claude-code", cloudSessionId: "session_01CNrG8QvmZWiHRpzs2xhqhN", repository: "plmercereau/wrekin" },
    });
    expect(sent?.params).not.toHaveProperty("directory");
    expect(result.structuredContent).toEqual(readyJob());
    expect(textOf(result)).toContain("wrekin attach wrekin-01CN");
    expect(textOf(result)).not.toMatch(/\$ |\x1b\[/); // no terminal output, ever
  });

  it("explains needs_attention and asks for approve_repository", async () => {
    hub.answer(mine.id, "get_teleport_status", {
      ok: true,
      data: readyJob({
        state: "needs_attention",
        session: null,
        ready: null,
        attention: { kind: "approve_repository", message: "The repository is not on this machine.", repositoryUrl: "https://github.com/plmercereau/wrekin", proposedPath: "/home/me/projects/wrekin" },
      }),
    });
    const result = await call("get_teleport_status", { node: mine.id, jobId: "job_01J" });
    expect(result.isError).toBeFalsy();
    expect(textOf(result)).toContain("needs_attention");
    expect(textOf(result)).toContain("approve_repository");
    expect(textOf(result)).toContain("/home/me/projects/wrekin");
  });

  it("flags an unverified ready job and points at the attach command", async () => {
    hub.answer(mine.id, "get_teleport_status", { ok: true, data: readyJob({ ready: { confirmed: false, detail: "prompt not seen within 60 s" } }) });
    const result = await call("get_teleport_status", { node: "workstation", jobId: "job_01J" });
    expect(textOf(result)).toContain("could not be verified");
    // The teleport hands the work off: the cloud conversation must say it cannot act on the machine.
    expect(textOf(result)).toContain("not in this conversation");
    expect(textOf(result)).toContain("wrekin attach wrekin-01CN");
  });

  it("sends the user to the Remote Control session of a ready job that has one", async () => {
    hub.answer(mine.id, "get_teleport_status", {
      ok: true,
      data: readyJob({
        session: { terminal: "tmux", name: "wrekin-01CN", attach: "wrekin attach wrekin-01CN", remoteControl: "workstation wrekin-01CN" },
        ready: { confirmed: true, detail: "prompt visible, Remote Control active" },
      }),
    });
    const text = textOf(await call("get_teleport_status", { node: "workstation", jobId: "job_01J" }));
    expect(text).toContain('listed as "workstation wrekin-01CN"');
    expect(text).toContain("claude.ai/code");
    expect(text).toContain("switch to the Remote Control session");
  });

  it("approve_repository passes the decision through and reports a node error", async () => {
    hub.answer(mine.id, "approve_repository", { ok: false, error: { code: "attention_not_pending", message: "the job is not waiting" } });
    const result = await call("approve_repository", { node: "workstation", jobId: "job_01J", approved: false });
    expect(result.isError).toBe(true);
    expect(result.structuredContent).toEqual({ error: { code: "attention_not_pending", message: "the job is not waiting" } });
    expect(hub.commands[hub.commands.length - 1]).toMatchObject({ name: "approve_repository", params: { jobId: "job_01J", approved: false } });
  });

  it("reports an unexpected job shape as internal instead of passing it through", async () => {
    hub.answer(mine.id, "get_teleport_status", { ok: true, data: { id: "job_x", state: "done" } });
    const result = await call("get_teleport_status", { node: "workstation", jobId: "job_x" });
    expect(result.isError).toBe(true);
    expect((result.structuredContent as { error: { code: string } }).error.code).toBe("internal");
  });
});

describe("POST /mcp", () => {
  const { fetcher } = nodeWorker();

  it("challenges an unauthenticated request with the protected resource metadata", async () => {
    const res = await fetcher("/mcp", {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(res.status).toBe(401);
    const challenge = res.headers.get("www-authenticate") ?? "";
    expect(challenge).toMatch(/^Bearer /);
    expect(challenge).toContain('resource_metadata="http://localhost:8787/.well-known/oauth-protected-resource/mcp"');
    const body = (await res.json()) as { jsonrpc: string; error: { code: number } };
    expect(body.jsonrpc).toBe("2.0");
    expect(body.error.code).toBe(-32000);
  });

  it("rejects a bad bearer token", async () => {
    const res = await fetcher("/mcp", {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream", authorization: "Bearer not-a-jwt" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(res.status).toBe(401);
  });

  it("answers 405 to GET and DELETE", async () => {
    expect((await fetcher("/mcp")).status).toBe(405);
    expect((await fetcher("/mcp", { method: "DELETE" })).status).toBe(405);
  });
});
