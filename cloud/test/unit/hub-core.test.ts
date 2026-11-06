import { beforeEach, describe, expect, it, vi } from "vitest";
import { encodeBase64, verifyEd25519 } from "../../src/crypto";
import { HubCore, type Attachment, type HubSocket } from "../../src/hub/core";
import { authMessage, closeCodes } from "../../src/protocol";
import { alternateSpelling, generateNodeKey } from "../helpers/keys";

class FakeSocket implements HubSocket {
  sent: string[] = [];
  closed: { code: number; reason: string } | null = null;
  private attachment: Attachment | null = null;
  send(data: string) {
    this.sent.push(data);
  }
  close(code: number, reason: string) {
    this.closed = { code, reason };
  }
  getAttachment() {
    return this.attachment;
  }
  setAttachment(attachment: Attachment) {
    this.attachment = attachment;
  }
  /** The last frame sent, parsed. */
  last<T = Record<string, unknown>>(): T {
    return JSON.parse(this.sent[this.sent.length - 1] ?? "null") as T;
  }
}

function makeHub(options: { handshakeTimeoutMs?: number } = {}) {
  const sockets: FakeSocket[] = [];
  const touched: Array<{ nodeId: string; at: string }> = [];
  const core = new HubCore(
    {
      sockets: () => sockets.filter((s) => s.closed === null),
      verify: verifyEd25519,
      randomNonce: () => encodeBase64(crypto.getRandomValues(new Uint8Array(32))),
      now: () => new Date("2026-11-06T00:00:00Z"),
      onAuthenticated: async (nodeId, at) => {
        touched.push({ nodeId, at });
      },
    },
    options,
  );
  const open = (nodeId: string, publicKey: string) => {
    const socket = new FakeSocket();
    sockets.push(socket);
    core.onOpen(socket, { accountId: "acc_test", nodeId, publicKey });
    return socket;
  };
  const frame = (socket: FakeSocket, message: unknown) => core.onMessage(socket, JSON.stringify(message));
  return { core, sockets, touched, open, frame };
}

/** Runs the whole handshake for `nodeId` with `key` and returns the authenticated socket. */
async function connectAndAuth(hub: ReturnType<typeof makeHub>, nodeId: string, key: Awaited<ReturnType<typeof generateNodeKey>>) {
  const socket = hub.open(nodeId, key.publicKey);
  await hub.frame(socket, {
    v: 1,
    type: "hello",
    nodeId,
    publicKey: key.publicKey,
    name: "ws",
    agentVersion: "0.1.0",
    harnesses: [{ id: "claude-code", available: true, version: "2.1.0" }],
  });
  const challenge = socket.last<{ type: string; nonce: string }>();
  expect(challenge.type).toBe("challenge");
  await hub.frame(socket, { v: 1, type: "auth", signature: await key.sign(authMessage(nodeId, challenge.nonce)) });
  return socket;
}

describe("handshake", () => {
  let key: Awaited<ReturnType<typeof generateNodeKey>>;
  beforeEach(async () => {
    key = await generateNodeKey();
  });

  it("answers hello with a 32-byte nonce and a good signature with welcome", async () => {
    const hub = makeHub();
    const socket = hub.open("node_1", key.publicKey);
    expect(socket.sent).toHaveLength(0); // nothing before hello
    await hub.frame(socket, { v: 1, type: "hello", nodeId: "node_1", publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0" });
    const challenge = socket.last<{ type: string; nonce: string }>();
    expect(challenge.type).toBe("challenge");
    expect(atob(challenge.nonce)).toHaveLength(32);
    await hub.frame(socket, { v: 1, type: "auth", signature: await key.sign(authMessage("node_1", challenge.nonce)) });
    expect(socket.last()).toEqual({ v: 1, type: "welcome", accountId: "acc_test", nodeId: "node_1", pingSeconds: 30 });
    expect(socket.closed).toBeNull();
    expect(hub.touched).toEqual([{ nodeId: "node_1", at: "2026-11-06T00:00:00.000Z" }]);
    expect(hub.core.online()).toMatchObject([{ nodeId: "node_1", agentVersion: "0.1.0" }]);
  });

  it("closes 4401 on a wrong signature", async () => {
    const hub = makeHub();
    const socket = hub.open("node_1", key.publicKey);
    await hub.frame(socket, { v: 1, type: "hello", nodeId: "node_1", publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0" });
    const { nonce } = socket.last<{ nonce: string }>();
    const other = await generateNodeKey();
    await hub.frame(socket, { v: 1, type: "auth", signature: await other.sign(authMessage("node_1", nonce)) });
    expect(socket.closed?.code).toBe(closeCodes.authFailed);
    expect(socket.last().type).toBe("error");
    expect(hub.core.online()).toEqual([]);
  });

  it("closes 4401 when hello carries a public key other than the registered one", async () => {
    const hub = makeHub();
    const socket = hub.open("node_1", key.publicKey);
    const other = await generateNodeKey();
    await hub.frame(socket, { v: 1, type: "hello", nodeId: "node_1", publicKey: other.publicKey, name: "ws", agentVersion: "0.1.0" });
    expect(socket.closed?.code).toBe(closeCodes.authFailed);
  });

  it("accepts a hello whose public key is the registered one under another base64 spelling", async () => {
    const hub = makeHub();
    const spelled = alternateSpelling(key.publicKey);
    const registeredCanonical = hub.open("node_1", key.publicKey);
    await hub.frame(registeredCanonical, { v: 1, type: "hello", nodeId: "node_1", publicKey: spelled, name: "ws", agentVersion: "0.1.0" });
    expect(registeredCanonical.closed).toBeNull();
    expect(registeredCanonical.last().type).toBe("challenge");
    // And the other way round: a row written before keys were canonicalised.
    const registeredSpelled = hub.open("node_2", spelled);
    await hub.frame(registeredSpelled, { v: 1, type: "hello", nodeId: "node_2", publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0" });
    expect(registeredSpelled.closed).toBeNull();
    const { nonce } = registeredSpelled.last<{ nonce: string }>();
    await hub.frame(registeredSpelled, { v: 1, type: "auth", signature: await key.sign(authMessage("node_2", nonce)) });
    expect(registeredSpelled.last().type).toBe("welcome");
  });

  it("closes 4400 on a malformed frame or a frame out of order", async () => {
    const hub = makeHub();
    const garbage = hub.open("node_1", key.publicKey);
    await hub.core.onMessage(garbage, "{not json");
    expect(garbage.closed?.code).toBe(closeCodes.protocolError);

    const early = hub.open("node_1", key.publicKey);
    await hub.frame(early, { v: 1, type: "auth", signature: "c2ln" });
    expect(early.closed?.code).toBe(closeCodes.protocolError);

    const wrongVersion = hub.open("node_1", key.publicKey);
    await hub.frame(wrongVersion, { v: 2, type: "hello" });
    expect(wrongVersion.closed?.code).toBe(closeCodes.protocolError);
  });

  it("ignores frames of an unknown type", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", key);
    await hub.frame(socket, { v: 1, type: "telemetry", cpu: 3 });
    expect(socket.closed).toBeNull();
  });

  it("answers ping with pong", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", key);
    await hub.frame(socket, { v: 1, type: "ping" });
    expect(socket.last()).toEqual({ v: 1, type: "pong" });
  });

  it("closes 4400 a socket that has not authenticated within the handshake timeout", async () => {
    const hub = makeHub({ handshakeTimeoutMs: 20 });
    const socket = hub.open("node_1", key.publicKey);
    await new Promise((r) => setTimeout(r, 60));
    expect(socket.closed?.code).toBe(closeCodes.protocolError);
    const quick = await connectAndAuth(hub, "node_1", key);
    await new Promise((r) => setTimeout(r, 60));
    expect(quick.closed).toBeNull();
  });

  it("replaces an older connection of the same node with 4409", async () => {
    const hub = makeHub();
    const first = await connectAndAuth(hub, "node_1", key);
    const second = await connectAndAuth(hub, "node_1", key);
    expect(first.closed?.code).toBe(closeCodes.replaced);
    expect(second.closed).toBeNull();
    expect(hub.core.online()).toHaveLength(1);
  });
});

describe("evict", () => {
  it("closes every socket of the node with 4404, fails its pending commands and reports it offline", async () => {
    const hub = makeHub();
    const key = await generateNodeKey();
    const other = await connectAndAuth(hub, "node_2", await generateNodeKey());
    const socket = await connectAndAuth(hub, "node_1", key);
    const halfway = hub.open("node_1", key.publicKey); // a reconnection still in its handshake
    const pending = hub.core.command("node_1", "ping", {}, 5000);

    hub.core.evict("node_1");

    expect(socket.closed?.code).toBe(closeCodes.unknownNode);
    expect(socket.last()).toMatchObject({ type: "error", code: "unknown_node" });
    expect(halfway.closed?.code).toBe(closeCodes.unknownNode);
    expect(other.closed).toBeNull();
    expect(await pending).toMatchObject({ ok: false, error: { code: "node_offline" } });
    expect(hub.core.online()).toMatchObject([{ nodeId: "node_2" }]);
    expect(await hub.core.command("node_1", "ping", {}, 1000)).toMatchObject({ ok: false, error: { code: "node_offline" } });
  });

  it("is a no-op for a node without a socket", () => {
    const hub = makeHub();
    expect(() => hub.core.evict("node_absent")).not.toThrow();
    expect(hub.core.online()).toEqual([]);
  });
});

describe("commands", () => {
  it("round-trips a command and its result", async () => {
    const hub = makeHub();
    const key = await generateNodeKey();
    const socket = await connectAndAuth(hub, "node_1", key);
    const pending = hub.core.command("node_1", "ping", {}, 1000);
    const sent = socket.last<{ type: string; id: string; name: string; params: unknown }>();
    expect(sent).toMatchObject({ v: 1, type: "command", name: "ping", params: {} });
    await hub.frame(socket, { v: 1, type: "result", id: sent.id, ok: true, data: { at: "now" } });
    expect(await pending).toEqual({ ok: true, data: { at: "now" } });
  });

  it("passes a node error through", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", await generateNodeKey());
    const pending = hub.core.command("node_1", "get_teleport_status", { jobId: "job_x" }, 1000);
    const { id } = socket.last<{ id: string }>();
    await hub.frame(socket, { v: 1, type: "result", id, ok: false, error: { code: "unknown_job", message: "no such job" } });
    expect(await pending).toEqual({ ok: false, error: { code: "unknown_job", message: "no such job" } });
  });

  it("fails at once with node_offline when the node has no authenticated socket", async () => {
    const hub = makeHub();
    const key = await generateNodeKey();
    expect(await hub.core.command("node_1", "ping", {}, 1000)).toMatchObject({ ok: false, error: { code: "node_offline" } });
    const halfway = hub.open("node_1", key.publicKey);
    await hub.frame(halfway, { v: 1, type: "hello", nodeId: "node_1", publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0" });
    expect(await hub.core.command("node_1", "ping", {}, 1000)).toMatchObject({ ok: false, error: { code: "node_offline" } });
  });

  it("answers node_timeout when the node does not reply in time", async () => {
    const hub = makeHub();
    await connectAndAuth(hub, "node_1", await generateNodeKey());
    const started = Date.now();
    const result = await hub.core.command("node_1", "ping", {}, 100);
    expect(result).toMatchObject({ ok: false, error: { code: "node_timeout" } });
    expect(Date.now() - started).toBeLessThan(2000);
  });

  it("fails pending commands with node_offline when the socket closes", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", await generateNodeKey());
    const pending = hub.core.command("node_1", "ping", {}, 5000);
    socket.closed = { code: 1006, reason: "gone" };
    hub.core.onClose(socket, 1006, "gone");
    expect(await pending).toMatchObject({ ok: false, error: { code: "node_offline" } });
    expect(hub.core.online()).toEqual([]);
  });

  it("accepts a result only from the connection the command was sent to", async () => {
    const hub = makeHub();
    const a = await connectAndAuth(hub, "node_a", await generateNodeKey());
    const b = await connectAndAuth(hub, "node_b", await generateNodeKey());
    let settled = false;
    const pending = hub.core.command("node_a", "ping", {}, 1000).then((result) => {
      settled = true;
      return result;
    });
    const { id } = a.last<{ id: string }>();
    expect(b.sent.some((frame) => frame.includes(id))).toBe(false); // B never saw the command

    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      await hub.frame(b, { v: 1, type: "result", id, ok: true, data: { forged: true } });
      await new Promise((r) => setTimeout(r, 0));
      expect(settled).toBe(false);
      expect(b.closed).toBeNull(); // ignored, not a protocol failure
      expect(warn).toHaveBeenCalledTimes(1);
      expect(warn.mock.calls[0]?.[0]).toContain(id);
    } finally {
      warn.mockRestore();
    }

    await hub.frame(a, { v: 1, type: "result", id, ok: true, data: { at: "now" } });
    expect(await pending).toEqual({ ok: true, data: { at: "now" } });
    expect(hub.core.online()).toHaveLength(2);
  });

  it("refuses parameters the command's schema rejects without touching the socket", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", await generateNodeKey());
    const before = socket.sent.length;
    expect(await hub.core.command("node_1", "teleport", { harness: 1 }, 1000)).toMatchObject({ ok: false, error: { code: "invalid_params" } });
    expect(await hub.core.command("node_1", "get_teleport_status", {}, 1000)).toMatchObject({ ok: false, error: { code: "invalid_params" } });
    expect(await hub.core.command("node_1", "approve_repository", { jobId: "job_1", approved: "yes" }, 1000)).toMatchObject({ ok: false, error: { code: "invalid_params" } });
    expect(socket.sent).toHaveLength(before);
    // Invalid parameters are reported before the node's reachability.
    expect(await hub.core.command("node_absent", "teleport", {}, 1000)).toMatchObject({ ok: false, error: { code: "invalid_params" } });
  });

  it("sends only the parameters the command's schema knows", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", await generateNodeKey());
    const pending = hub.core.command("node_1", "teleport", { harness: "claude-code", cloudSessionId: "session_1", surprise: true }, 1000);
    const sent = socket.last<{ id: string; params: Record<string, unknown> }>();
    expect(sent.params).toEqual({ harness: "claude-code", cloudSessionId: "session_1" });
    await hub.frame(socket, { v: 1, type: "result", id: sent.id, ok: true, data: null });
    expect(await pending).toEqual({ ok: true, data: null });
  });

  it("ignores a result for an unknown command id and a malformed result", async () => {
    const hub = makeHub();
    const socket = await connectAndAuth(hub, "node_1", await generateNodeKey());
    await hub.frame(socket, { v: 1, type: "result", id: "cmd_unknown", ok: true, data: null });
    expect(socket.closed).toBeNull();
    await hub.frame(socket, { v: 1, type: "result", id: "cmd_x", ok: false, error: { code: "made_up", message: "x" } });
    expect(socket.closed?.code).toBe(closeCodes.protocolError);
  });
});
