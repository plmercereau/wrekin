import { describe, expect, it } from "vitest";
import { FakeHub } from "../helpers/fake-hub";
import { generateNodeKey } from "../helpers/keys";
import { MemoryNodeStore } from "../helpers/memory-nodes";
import { nodeWorker } from "../helpers/node-app";
import { createTestSession } from "../helpers/session";
import { nodesSuite } from "../shared/nodes-suite";

const hub = new FakeHub();
const nodes = new MemoryNodeStore();
const { fetcher, services, caller } = nodeWorker({ nodes, hub: hub.factory });

nodesSuite(
  fetcher,
  (label) => createTestSession(services.auth, label),
  async (headers) => (await caller(headers)).nodes.list(),
);

describe("nodes.list with the hub", () => {
  it("merges the stored rows with the online state", async () => {
    const { userId, headers } = await createTestSession(services.auth, "online");
    const key = await generateNodeKey();
    const created = await fetcher("/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ name: "desk", publicKey: key.publicKey, agentVersion: "0.1.0" }),
    });
    const { nodeId } = (await created.json()) as { nodeId: string };
    hub.setOnline(userId, [
      { nodeId, connectedAt: "2026-11-06T00:00:00Z", agentVersion: "0.2.0", harnesses: [{ id: "claude-code", available: true }] },
      { nodeId: "node_stale_socket_of_a_deleted_node", connectedAt: "2026-11-06T00:00:00Z" },
    ]);
    const list = await (await caller(headers)).nodes.list();
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({
      id: nodeId,
      online: true,
      connectedAt: "2026-11-06T00:00:00Z",
      agentVersion: "0.2.0",
      harnesses: [{ id: "claude-code", available: true }],
    });
  });
});
