import { TRPCError } from "@trpc/server";
import { describe, expect, it } from "vitest";
import { unregisterMessage } from "../../src/nodes/service";
import { FakeHub } from "../helpers/fake-hub";
import { generateNodeKey } from "../helpers/keys";
import { MemoryNodeStore } from "../helpers/memory-nodes";
import { nodeWorker } from "../helpers/node-app";
import { createTestSession } from "../helpers/session";

// The router through createCallerFactory: memory auth decides who the caller is, the fake hub
// says who is online. The same procedures serve the browser over /api/trpc.
const hub = new FakeHub();
const { services, caller } = nodeWorker({ nodes: new MemoryNodeStore(), hub: hub.factory });
const session = (label: string) => createTestSession(services.auth, label);

const code = async (promise: Promise<unknown>): Promise<{ code: string; message: string }> => {
  try {
    await promise;
  } catch (error) {
    if (error instanceof TRPCError) return { code: error.code, message: error.message };
    throw error;
  }
  throw new Error("expected the procedure to throw");
};

describe("session.me", () => {
  it("is null without a session", async () => {
    expect(await (await caller()).session.me()).toBeNull();
  });

  it("is the bearer of the session token", async () => {
    const { userId, headers } = await session("me");
    expect(await (await caller(headers)).session.me()).toMatchObject({ id: userId, name: "me" });
  });
});

describe("nodes", () => {
  it("refuses every protected procedure without a session with UNAUTHORIZED", async () => {
    const anonymous = await caller();
    const key = await generateNodeKey();
    expect((await code(anonymous.nodes.list())).code).toBe("UNAUTHORIZED");
    expect((await code(anonymous.nodes.remove({ id: "node_x" }))).code).toBe("UNAUTHORIZED");
    expect((await code(anonymous.nodes.register({ name: "ws", publicKey: key.publicKey }))).code).toBe("UNAUTHORIZED");
  });

  it("registers, lists and removes a node of the caller's account", async () => {
    const { userId, headers } = await session("owner");
    const mine = await caller(headers);
    const key = await generateNodeKey();
    const created = await mine.nodes.register({ name: "studio", publicKey: key.publicKey, platform: "linux/amd64" });
    expect(created).toEqual({ nodeId: expect.stringMatching(/^node_/), accountId: userId });

    hub.setOnline(userId, [{ nodeId: created.nodeId, connectedAt: "2026-11-06T00:00:00Z" }]);
    const listed = await mine.nodes.list();
    expect(listed).toHaveLength(1);
    expect(listed[0]).toMatchObject({ id: created.nodeId, name: "studio", online: true, platform: "linux/amd64" });
    expect(listed[0]).not.toHaveProperty("publicKey");

    expect(await mine.nodes.remove({ id: created.nodeId })).toEqual({ id: created.nodeId });
    expect((await code(mine.nodes.remove({ id: created.nodeId }))).code).toBe("NOT_FOUND");
    expect(await mine.nodes.list()).toHaveLength(0);
  });

  it("never removes another account's node", async () => {
    const a = await caller((await session("a")).headers);
    const b = await caller((await session("b")).headers);
    const { nodeId } = await a.nodes.register({ name: "theirs", publicKey: (await generateNodeKey()).publicKey });
    expect((await code(b.nodes.remove({ id: nodeId }))).code).toBe("NOT_FOUND");
    expect(await a.nodes.list()).toHaveLength(1);
  });

  it("maps the service's refusals to tRPC codes that carry the REST code", async () => {
    const mine = await caller((await session("dup")).headers);
    await mine.nodes.register({ name: "laptop", publicKey: (await generateNodeKey()).publicKey });
    const taken = await code(mine.nodes.register({ name: "laptop", publicKey: (await generateNodeKey()).publicKey }));
    expect(taken).toMatchObject({ code: "CONFLICT", message: expect.stringContaining("name_taken") });
    const badKey = await code(mine.nodes.register({ name: "other", publicKey: btoa("short") }));
    expect(badKey).toMatchObject({ code: "BAD_REQUEST", message: expect.stringContaining("invalid_public_key") });
  });

  it("unregisters with the node's signature and no session", async () => {
    const owner = await caller((await session("headless")).headers);
    const key = await generateNodeKey();
    const { nodeId } = await owner.nodes.register({ name: "box", publicKey: key.publicKey });
    const anonymous = await caller();

    const stale = new Date(Date.now() - 10 * 60 * 1000).toISOString();
    const staleAttempt = await code(anonymous.nodes.unregister({ nodeId, timestamp: stale, signature: await key.sign(unregisterMessage(nodeId, stale)) }));
    expect(staleAttempt).toMatchObject({ code: "UNAUTHORIZED", message: expect.stringContaining("stale_timestamp") });

    const timestamp = new Date().toISOString();
    const forged = await code(anonymous.nodes.unregister({ nodeId, timestamp, signature: await (await generateNodeKey()).sign(unregisterMessage(nodeId, timestamp)) }));
    expect(forged).toMatchObject({ code: "UNAUTHORIZED", message: expect.stringContaining("bad_signature") });

    expect(await anonymous.nodes.unregister({ nodeId, timestamp, signature: await key.sign(unregisterMessage(nodeId, timestamp)) })).toEqual({ nodeId });
    expect((await code(anonymous.nodes.unregister({ nodeId, timestamp, signature: await key.sign(unregisterMessage(nodeId, timestamp)) }))).code).toBe("NOT_FOUND");
  });
});
