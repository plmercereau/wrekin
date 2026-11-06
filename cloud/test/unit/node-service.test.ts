import { describe, expect, it } from "vitest";
import { listNodes, NodeServiceError, parseTimestamp, registerNode, registerSchema, removeNode, toNodeView, unregisterMessage, unregisterNode } from "../../src/nodes/service";
import { FakeHub } from "../helpers/fake-hub";
import { alternateSpelling, generateNodeKey } from "../helpers/keys";
import { MemoryNodeStore } from "../helpers/memory-nodes";

// The one implementation behind POST /api/nodes, POST /api/nodes/unregister and the tRPC procedures.
const hub = new FakeHub();
const services = { nodes: new MemoryNodeStore(), hub: hub.factory };

const refusal = async (promise: Promise<unknown>) => {
  try {
    await promise;
  } catch (error) {
    if (error instanceof NodeServiceError) return { status: error.status, code: error.code };
    throw error;
  }
  throw new Error("expected a NodeServiceError");
};

describe("parseTimestamp", () => {
  it.each([
    ["2026-11-06T00:00:00Z", Date.UTC(2026, 10, 6)],
    ["1794009600", 1794009600 * 1000],
    [1794009600, 1794009600 * 1000],
    ["yesterday", null],
    [Number.NaN, null],
  ])("%s → %s", (input, expected) => {
    expect(parseTimestamp(input)).toBe(expected);
  });
});

describe("registerSchema", () => {
  const key = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
  it.each(["work-station 2", "mbp", "Bureau Élodie", "東京-1", "a.b_c", "x".repeat(64), "  trimmed  "])("accepts %j", (name) => {
    expect(registerSchema.safeParse({ name, publicKey: key }).success).toBe(true);
  });

  it.each(["my\nnode", "tab\tbed", "node_abc", "NODE_abc", "node_", "-first", ".first", "x".repeat(65), "", "   ", "a/b", "a\u0000b", "emoji 🚀"])(
    "refuses %j",
    (name) => {
      expect(registerSchema.safeParse({ name, publicKey: key }).success).toBe(false);
    },
  );

  it("trims the name before the rule applies", () => {
    expect(registerSchema.parse({ name: "  trimmed  ", publicKey: key }).name).toBe("trimmed");
  });
});

describe("toNodeView", () => {
  it("replaces the key by its fingerprint and merges the live state", async () => {
    const key = await generateNodeKey();
    const node = await services.nodes.insertNode({ accountId: "acc_view", name: "view", publicKey: key.publicKey, agentVersion: "0.1.0" });
    const offline = await toNodeView(node, []);
    expect(offline).toMatchObject({ id: node.id, online: false, connectedAt: null, agentVersion: "0.1.0", harnesses: [] });
    expect(offline.fingerprint).toMatch(/^[0-9a-f]{4}(:[0-9a-f]{4}){3}$/);
    expect(offline).not.toHaveProperty("publicKey");
    const live = await toNodeView(node, [{ nodeId: node.id, connectedAt: "2026-11-06T00:00:00Z", agentVersion: "0.2.0", harnesses: [{ id: "claude-code", available: true }] }]);
    expect(live).toMatchObject({ online: true, connectedAt: "2026-11-06T00:00:00Z", agentVersion: "0.2.0", harnesses: [{ id: "claude-code", available: true }] });
  });
});

describe("registerNode and removeNode", () => {
  it("refuses a key that is not 32 raw Ed25519 bytes before touching the store", async () => {
    expect(await refusal(registerNode(services, "acc_reg", { name: "k", publicKey: btoa("short") }))).toEqual({ status: 400, code: "invalid_public_key" });
  });

  it("refuses a duplicate name on the account and a key registered anywhere", async () => {
    const key = await generateNodeKey();
    const { nodeId } = await registerNode(services, "acc_reg", { name: "laptop", publicKey: key.publicKey });
    expect(await refusal(registerNode(services, "acc_reg", { name: "laptop", publicKey: (await generateNodeKey()).publicKey }))).toEqual({ status: 409, code: "name_taken" });
    expect(await refusal(registerNode(services, "acc_other", { name: "elsewhere", publicKey: key.publicKey }))).toEqual({ status: 409, code: "key_taken" });
    expect(await removeNode(services, "acc_other", nodeId)).toBe(false);
    expect(hub.evicted).not.toContain(`acc_other/${nodeId}`); // nothing was removed, nothing is evicted
    expect(await removeNode(services, "acc_reg", nodeId)).toBe(true);
    expect(hub.evicted).toContain(`acc_reg/${nodeId}`);
    expect(await listNodes(services, "acc_reg")).toHaveLength(0);
  });
});

describe("registerNode stores the key in canonical base64", () => {
  it("refuses the same 32 bytes under another spelling and stores the canonical one", async () => {
    const key = await generateNodeKey();
    const other = alternateSpelling(key.publicKey);
    const { nodeId } = await registerNode(services, "acc_canon", { name: "spelled", publicKey: other });
    expect((await services.nodes.findNodeById(nodeId))?.publicKey).toBe(key.publicKey);
    expect(await refusal(registerNode(services, "acc_canon", { name: "again", publicKey: key.publicKey }))).toEqual({ status: 409, code: "key_taken" });
    expect(await refusal(registerNode(services, "acc_elsewhere", { name: "again", publicKey: other }))).toEqual({ status: 409, code: "key_taken" });
  });
});

describe("unregisterNode", () => {
  it("verifies the signature over the fresh timestamp with the stored key", async () => {
    const key = await generateNodeKey();
    const { nodeId } = await registerNode(services, "acc_unreg", { name: "box", publicKey: key.publicKey });
    const timestamp = new Date().toISOString();
    expect(await refusal(unregisterNode(services, { nodeId: "node_nope", timestamp, signature: await key.sign(unregisterMessage("node_nope", timestamp)) }))).toEqual({ status: 404, code: "unknown_node" });
    expect(await refusal(unregisterNode(services, { nodeId, timestamp: "2020-01-01T00:00:00Z", signature: await key.sign(unregisterMessage(nodeId, "2020-01-01T00:00:00Z")) }))).toEqual({ status: 401, code: "stale_timestamp" });
    expect(await refusal(unregisterNode(services, { nodeId, timestamp, signature: await (await generateNodeKey()).sign(unregisterMessage(nodeId, timestamp)) }))).toEqual({ status: 401, code: "bad_signature" });
    await unregisterNode(services, { nodeId, timestamp, signature: await key.sign(unregisterMessage(nodeId, timestamp)) });
    expect(await listNodes(services, "acc_unreg")).toHaveLength(0);
    expect(hub.evicted).toContain(`acc_unreg/${nodeId}`);
  });
});
