import { env } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { getDb } from "../src/db/kysely";
import { KyselyNodeStore } from "../src/db/nodes";
import { ACCOUNT_ID_HEADER, PUBLIC_KEY_HEADER } from "../src/hub/headers";
import { HUB_ORIGIN, hubFor } from "../src/hub/client";
import { nodesChangedFrame } from "../src/live";
import { authMessage, closeCodes } from "../src/protocol";
import { generateNodeKey } from "./helpers/keys";

// The real Durable Object inside workerd, driven over a WebSocket by the test.

type Frame = Record<string, unknown>;

async function openSocket(accountId: string, nodeId: string, publicKey: string) {
  const stub = env.ACCOUNT_HUB.get(env.ACCOUNT_HUB.idFromName(accountId));
  const res = await stub.fetch(`${HUB_ORIGIN}/connect?nodeId=${encodeURIComponent(nodeId)}`, {
    headers: { Upgrade: "websocket", [PUBLIC_KEY_HEADER]: publicKey, [ACCOUNT_ID_HEADER]: accountId },
  });
  return track(res);
}

/** The test's end of a 101 answer: buffered frames, and promises for the next frame and the close. */
function track(res: Response) {
  expect(res.status).toBe(101);
  const ws = res.webSocket;
  if (!ws) throw new Error("no WebSocket on the 101 response");
  ws.accept();
  const frames: Frame[] = [];
  const waiters: Array<(frame: Frame) => void> = [];
  let closed: { code: number; reason: string } | null = null;
  const closeWaiters: Array<() => void> = [];
  ws.addEventListener("message", (event) => {
    const frame = JSON.parse(String(event.data)) as Frame;
    const waiter = waiters.shift();
    if (waiter) waiter(frame);
    else frames.push(frame);
  });
  ws.addEventListener("close", (event) => {
    closed = { code: event.code, reason: event.reason };
    for (const w of closeWaiters.splice(0)) w();
  });
  const next = () =>
    new Promise<Frame>((resolve, reject) => {
      const buffered = frames.shift();
      if (buffered) return resolve(buffered);
      const timer = setTimeout(() => reject(new Error("no frame within 2 s")), 2000);
      waiters.push((frame) => {
        clearTimeout(timer);
        resolve(frame);
      });
    });
  const waitClose = () =>
    new Promise<{ code: number; reason: string }>((resolve, reject) => {
      if (closed) return resolve(closed);
      const timer = setTimeout(() => reject(new Error("not closed within 2 s")), 2000);
      closeWaiters.push(() => {
        clearTimeout(timer);
        resolve(closed as unknown as { code: number; reason: string });
      });
    });
  return { ws, next, waitClose, send: (frame: Frame) => ws.send(JSON.stringify(frame)) };
}

async function authenticate(accountId: string, nodeId: string, key: Awaited<ReturnType<typeof generateNodeKey>>) {
  const socket = await openSocket(accountId, nodeId, key.publicKey);
  socket.send({ v: 1, type: "hello", nodeId, publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0", harnesses: [] });
  const challenge = await socket.next();
  expect(challenge.type).toBe("challenge");
  socket.send({ v: 1, type: "auth", signature: await key.sign(authMessage(nodeId, challenge.nonce as string)) });
  const welcome = await socket.next();
  expect(welcome).toMatchObject({ v: 1, type: "welcome", accountId, nodeId, pingSeconds: 30 });
  return socket;
}

describe("AccountHub", () => {
  it("completes the handshake, reports the node online and touches last_seen_at", async () => {
    const key = await generateNodeKey();
    const nodes = new KyselyNodeStore(getDb(env));
    // The node table references user(id); Better Auth owns that table, so insert the row raw.
    await env.DB.prepare(
      'insert into "user" (id, name, email, "emailVerified", "createdAt", "updatedAt") values (?, ?, ?, 1, ?, ?)',
    )
      .bind("acc_hub", "hub", "hub@example.test", new Date().toISOString(), new Date().toISOString())
      .run();
    const node = await nodes.insertNode({ accountId: "acc_hub", name: "ws", publicKey: key.publicKey });
    await authenticate("acc_hub", node.id, key);
    const online = await hubFor(env, "acc_hub").online();
    expect(online).toMatchObject([{ nodeId: node.id, agentVersion: "0.1.0" }]);
    const row = await nodes.findNodeById(node.id);
    expect(row?.lastSeenAt).toBeTruthy();
  });

  it("closes 4401 on a bad signature", async () => {
    const key = await generateNodeKey();
    const intruder = await generateNodeKey();
    const socket = await openSocket("acc_bad", "node_bad", key.publicKey);
    socket.send({ v: 1, type: "hello", nodeId: "node_bad", publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0" });
    const challenge = await socket.next();
    socket.send({ v: 1, type: "auth", signature: await intruder.sign(authMessage("node_bad", challenge.nonce as string)) });
    const closed = await socket.waitClose();
    expect(closed.code).toBe(closeCodes.authFailed);
  });

  it("routes a command to the node and returns its result", async () => {
    const key = await generateNodeKey();
    const socket = await authenticate("acc_cmd", "node_cmd", key);
    const pending = hubFor(env, "acc_cmd").command<{ at: string }>("node_cmd", "ping", {}, 2000);
    const command = await socket.next();
    expect(command).toMatchObject({ v: 1, type: "command", name: "ping" });
    socket.send({ v: 1, type: "result", id: command.id, ok: true, data: { at: "now" } });
    expect(await pending).toEqual({ ok: true, data: { at: "now" } });
  });

  it("refuses parameters the command's schema rejects without sending anything to the node", async () => {
    const key = await generateNodeKey();
    const socket = await authenticate("acc_params", "node_params", key);
    const hub = hubFor(env, "acc_params");
    expect(await hub.command("node_params", "teleport", { harness: 1 }, 2000)).toMatchObject({ ok: false, error: { code: "invalid_params" } });
    // The next frame the node sees is the valid command that follows, so nothing was sent for the invalid one.
    const pending = hub.command("node_params", "ping", {}, 2000);
    const command = await socket.next();
    expect(command).toMatchObject({ v: 1, type: "command", name: "ping" });
    socket.send({ v: 1, type: "result", id: command.id, ok: true, data: null });
    expect(await pending).toEqual({ ok: true, data: null });
  });

  it("answers node_offline at once for a node without a socket", async () => {
    const result = await hubFor(env, "acc_offline").command("node_absent", "ping", {}, 2000);
    expect(result).toMatchObject({ ok: false, error: { code: "node_offline" } });
  });

  it("answers node_timeout when the node stays silent", async () => {
    const key = await generateNodeKey();
    await authenticate("acc_timeout", "node_silent", key);
    const result = await hubFor(env, "acc_timeout").command("node_silent", "ping", {}, 100);
    expect(result).toMatchObject({ ok: false, error: { code: "node_timeout" } });
  });

  it("replaces the previous socket of the same node with 4409", async () => {
    const key = await generateNodeKey();
    const first = await authenticate("acc_dup", "node_dup", key);
    await authenticate("acc_dup", "node_dup", key);
    const closed = await first.waitClose();
    expect(closed.code).toBe(closeCodes.replaced);
    expect(await hubFor(env, "acc_dup").online()).toHaveLength(1);
  });

  it("evicts a node: its socket closes with 4404 and it is no longer online", async () => {
    const key = await generateNodeKey();
    const socket = await authenticate("acc_evict", "node_evict", key);
    const hub = hubFor(env, "acc_evict");
    expect(await hub.online()).toHaveLength(1);
    await hub.evict("node_evict");
    const closed = await socket.waitClose();
    expect(closed.code).toBe(closeCodes.unknownNode);
    expect(await hub.online()).toEqual([]);
  });

  it("answers ping with pong without waking the handler", async () => {
    const key = await generateNodeKey();
    const socket = await authenticate("acc_ping", "node_ping", key);
    socket.ws.send('{"v":1,"type":"ping"}');
    expect(await socket.next()).toEqual({ v: 1, type: "pong" });
  });
});

describe("AccountHub's live channel", () => {
  const watch = async (accountId: string) =>
    track(await hubFor(env, accountId).watch(new Request("https://relay.test/api/nodes/live", { headers: { Upgrade: "websocket" } })));
  const changed = JSON.parse(nodesChangedFrame) as Frame;

  it("tells the account's pages when a node authenticates, disconnects, registers or is evicted", async () => {
    const page = await watch("acc_live");
    const key = await generateNodeKey();
    const node = await authenticate("acc_live", "node_live", key);
    expect(await page.next()).toEqual(changed);
    node.ws.close(1000, "bye");
    expect(await page.next()).toEqual(changed);
    await hubFor(env, "acc_live").changed();
    expect(await page.next()).toEqual(changed);
    await hubFor(env, "acc_live").evict("node_live");
    expect(await page.next()).toEqual(changed);
  });

  it("is not a node: it is never online, and a stray frame neither closes it nor reaches the handshake", async () => {
    const page = await watch("acc_live_quiet");
    page.ws.send("{not json");
    page.ws.send('{"v":1,"type":"hello"}');
    expect(await hubFor(env, "acc_live_quiet").online()).toEqual([]);
    // Still open: the next hint arrives.
    await hubFor(env, "acc_live_quiet").changed();
    expect(await page.next()).toEqual(changed);
  });

  it("hears nothing of another account", async () => {
    const page = await watch("acc_live_a");
    await hubFor(env, "acc_live_b").changed();
    await hubFor(env, "acc_live_a").changed();
    // The first frame is account A's own hint; had B's reached it, it would be followed by a second.
    expect(await page.next()).toEqual(changed);
    await expect(page.next()).rejects.toThrow("no frame");
  });
});
