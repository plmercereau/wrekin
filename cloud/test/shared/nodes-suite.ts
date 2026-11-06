import { describe, expect, it } from "vitest";
import { unregisterMessage } from "../../src/api/nodes";
import { generateNodeKey } from "../helpers/keys";
import type { Fetcher } from "./auth-suite";

export type SessionFactory = (label: string) => Promise<{ userId: string; headers: Record<string, string> }>;

const json = (body: unknown, headers: Record<string, string> = {}): RequestInit => ({
  method: "POST",
  headers: { "content-type": "application/json", ...headers },
  body: JSON.stringify(body),
});

/** Assertions shared by the plain-Node and the workerd runs of the node registration API. */
export function nodesSuite(fetcher: Fetcher, newSession: SessionFactory) {
  describe("node registration", () => {
    it("refuses an anonymous registration", async () => {
      const key = await generateNodeKey();
      const res = await fetcher("/api/nodes", json({ name: "ws", publicKey: key.publicKey }));
      expect(res.status).toBe(401);
    });

    it("registers a node for the session's account and lists it", async () => {
      const { userId, headers } = await newSession("alice");
      const key = await generateNodeKey();
      const res = await fetcher(
        "/api/nodes",
        json({ name: "workstation", publicKey: key.publicKey, agentVersion: "0.1.0", platform: "darwin/arm64" }, headers),
      );
      expect(res.status).toBe(201);
      const body = (await res.json()) as { nodeId: string; accountId: string };
      expect(body.nodeId).toMatch(/^node_[a-z2-7]{26}$/);
      expect(body.accountId).toBe(userId);

      const list = await fetcher("/api/nodes", { headers });
      expect(list.status).toBe(200);
      const { nodes } = (await list.json()) as { nodes: Array<Record<string, unknown>> };
      expect(nodes).toHaveLength(1);
      expect(nodes[0]).toMatchObject({ id: body.nodeId, name: "workstation", online: false, agentVersion: "0.1.0" });
      expect(nodes[0]?.fingerprint).toMatch(/^[0-9a-f]{4}(:[0-9a-f]{4}){3}$/);
      expect(nodes[0]).not.toHaveProperty("publicKey");
    });

    it("refuses a second node of the same name on the account with 409 name_taken", async () => {
      const { headers } = await newSession("bob");
      const first = await generateNodeKey();
      const second = await generateNodeKey();
      expect((await fetcher("/api/nodes", json({ name: "laptop", publicKey: first.publicKey }, headers))).status).toBe(201);
      const dup = await fetcher("/api/nodes", json({ name: "laptop", publicKey: second.publicKey }, headers));
      expect(dup.status).toBe(409);
      expect(((await dup.json()) as { error: string }).error).toBe("name_taken");
    });

    it("lets two accounts own a node of the same name", async () => {
      const a = await newSession("carol");
      const b = await newSession("dave");
      expect((await fetcher("/api/nodes", json({ name: "shared", publicKey: (await generateNodeKey()).publicKey }, a.headers))).status).toBe(201);
      expect((await fetcher("/api/nodes", json({ name: "shared", publicKey: (await generateNodeKey()).publicKey }, b.headers))).status).toBe(201);
    });

    it("refuses a public key that is not 32 base64 bytes", async () => {
      const { headers } = await newSession("erin");
      for (const publicKey of ["not base64!", btoa("short"), btoa("x".repeat(33))]) {
        const res = await fetcher("/api/nodes", json({ name: "k", publicKey }, headers));
        expect(res.status, publicKey).toBe(400);
      }
    });

    it("removes a node through the session", async () => {
      const { headers } = await newSession("frank");
      const created = await fetcher("/api/nodes", json({ name: "gone", publicKey: (await generateNodeKey()).publicKey }, headers));
      const { nodeId } = (await created.json()) as { nodeId: string };
      expect((await fetcher(`/api/nodes/${nodeId}`, { method: "DELETE", headers })).status).toBe(204);
      expect((await fetcher(`/api/nodes/${nodeId}`, { method: "DELETE", headers })).status).toBe(404);
      const other = await newSession("grace");
      expect((await fetcher(`/api/nodes/${nodeId}`, { method: "DELETE", headers: other.headers })).status).toBe(404);
    });
  });

  describe("signed unregistration", () => {
    async function registered(label: string) {
      const { headers } = await newSession(label);
      const key = await generateNodeKey();
      const res = await fetcher("/api/nodes", json({ name: label, publicKey: key.publicKey }, headers));
      const { nodeId } = (await res.json()) as { nodeId: string };
      return { nodeId, key, headers };
    }

    it("removes the node when the signature over the fresh timestamp verifies", async () => {
      const { nodeId, key, headers } = await registered("heidi");
      const timestamp = new Date().toISOString();
      const signature = await key.sign(unregisterMessage(nodeId, timestamp));
      const res = await fetcher("/api/nodes/unregister", json({ nodeId, timestamp, signature }));
      expect(res.status).toBe(204);
      const list = (await (await fetcher("/api/nodes", { headers })).json()) as { nodes: unknown[] };
      expect(list.nodes).toHaveLength(0);
    });

    it("accepts Unix seconds as the timestamp", async () => {
      const { nodeId, key } = await registered("ivan");
      const timestamp = Math.floor(Date.now() / 1000);
      const signature = await key.sign(unregisterMessage(nodeId, String(timestamp)));
      expect((await fetcher("/api/nodes/unregister", json({ nodeId, timestamp, signature }))).status).toBe(204);
    });

    it("refuses a stale timestamp with 401 even when correctly signed", async () => {
      const { nodeId, key } = await registered("judy");
      const timestamp = new Date(Date.now() - 10 * 60 * 1000).toISOString();
      const signature = await key.sign(unregisterMessage(nodeId, timestamp));
      const res = await fetcher("/api/nodes/unregister", json({ nodeId, timestamp, signature }));
      expect(res.status).toBe(401);
      expect(((await res.json()) as { error: string }).error).toBe("stale_timestamp");
    });

    it("refuses a signature from another key", async () => {
      const { nodeId } = await registered("kim");
      const intruder = await generateNodeKey();
      const timestamp = new Date().toISOString();
      const signature = await intruder.sign(unregisterMessage(nodeId, timestamp));
      const res = await fetcher("/api/nodes/unregister", json({ nodeId, timestamp, signature }));
      expect(res.status).toBe(401);
      expect(((await res.json()) as { error: string }).error).toBe("bad_signature");
    });

    it("answers 404 for an unknown node", async () => {
      const key = await generateNodeKey();
      const timestamp = new Date().toISOString();
      const signature = await key.sign(unregisterMessage("node_nope", timestamp));
      expect((await fetcher("/api/nodes/unregister", json({ nodeId: "node_nope", timestamp, signature }))).status).toBe(404);
    });
  });
}
