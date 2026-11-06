import { describe, expect, it } from "vitest";
import { unregisterMessage } from "../../src/nodes/service";
import { alternateSpelling, generateNodeKey } from "../helpers/keys";
import type { Fetcher } from "./auth-suite";

export type SessionFactory = (label: string) => Promise<{ userId: string; headers: Record<string, string>; cookieHeaders: Record<string, string> }>;

/** The account's nodes as the bearer of `headers` sees them (`nodes.list`, in-process or over HTTP). */
export type NodeLister = (headers: Record<string, string>) => Promise<Array<Record<string, unknown>>>;

const json = (body: unknown, headers: Record<string, string> = {}): RequestInit => ({
  method: "POST",
  headers: { "content-type": "application/json", ...headers },
  body: JSON.stringify(body),
});

/**
 * Assertions shared by the plain-Node and the workerd runs of the node API the Go agent
 * speaks: `POST /api/nodes` with a session, `POST /api/nodes/unregister` with a signature.
 */
export function nodesSuite(fetcher: Fetcher, newSession: SessionFactory, listNodes: NodeLister, baseURL: string) {
  describe("node registration", () => {
    it("refuses an anonymous registration", async () => {
      const key = await generateNodeKey();
      const res = await fetcher("/api/nodes", json({ name: "ws", publicKey: key.publicKey }));
      expect(res.status).toBe(401);
    });

    it("registers a node for the session's account and lists it", async () => {
      const { userId, headers, cookieHeaders } = await newSession("alice");
      const key = await generateNodeKey();
      const res = await fetcher(
        "/api/nodes",
        json({ name: "workstation", publicKey: key.publicKey, agentVersion: "0.1.0", platform: "darwin/arm64" }, headers),
      );
      expect(res.status).toBe(201);
      const body = (await res.json()) as { nodeId: string; accountId: string };
      expect(body.nodeId).toMatch(/^node_[a-z2-7]{26}$/);
      expect(body.accountId).toBe(userId);

      const nodes = await listNodes(cookieHeaders);
      expect(nodes).toHaveLength(1);
      expect(nodes[0]).toMatchObject({ id: body.nodeId, name: "workstation", online: false, agentVersion: "0.1.0" });
      expect(nodes[0]?.fingerprint).toMatch(/^[0-9a-f]{4}(:[0-9a-f]{4}){3}$/);
      expect(nodes[0]).not.toHaveProperty("publicKey");
    });

    it("refuses a second node of the same name on the account with 409 name_taken", async () => {
      const { headers, cookieHeaders } = await newSession("bob");
      const first = await generateNodeKey();
      const second = await generateNodeKey();
      expect((await fetcher("/api/nodes", json({ name: "laptop", publicKey: first.publicKey }, headers))).status).toBe(201);
      const dup = await fetcher("/api/nodes", json({ name: "laptop", publicKey: second.publicKey }, cookieHeaders));
      expect(dup.status).toBe(409);
      expect(((await dup.json()) as { error: string }).error).toBe("name_taken");
    });

    it("lets two accounts own a node of the same name", async () => {
      const a = await newSession("carol");
      const b = await newSession("dave");
      expect((await fetcher("/api/nodes", json({ name: "shared", publicKey: (await generateNodeKey()).publicKey }, a.headers))).status).toBe(201);
      expect((await fetcher("/api/nodes", json({ name: "shared", publicKey: (await generateNodeKey()).publicKey }, b.headers))).status).toBe(201);
    });

    it("refuses the same key under another base64 spelling with 409 key_taken", async () => {
      const { headers, cookieHeaders } = await newSession("faythe");
      const key = await generateNodeKey();
      const other = alternateSpelling(key.publicKey);
      expect(other).not.toBe(key.publicKey);
      expect((await fetcher("/api/nodes", json({ name: "first", publicKey: other }, headers))).status).toBe(201);
      const dup = await fetcher("/api/nodes", json({ name: "second", publicKey: key.publicKey }, cookieHeaders));
      expect(dup.status).toBe(409);
      expect(((await dup.json()) as { error: string }).error).toBe("key_taken");
    });

    it("refuses a public key that is not 32 base64 bytes", async () => {
      const { headers } = await newSession("erin");
      for (const publicKey of ["not base64!", btoa("short"), btoa("x".repeat(33))]) {
        const res = await fetcher("/api/nodes", json({ name: "k", publicKey }, headers));
        expect(res.status, publicKey).toBe(400);
        expect(((await res.json()) as { error: string }).error).toBe("invalid_public_key");
      }
    });

    it("accepts letters, digits, spaces, dots, underscores and dashes in a name, and nothing else", async () => {
      const { headers, cookieHeaders } = await newSession("gus");
      const ok = await fetcher("/api/nodes", json({ name: "work-station 2", publicKey: (await generateNodeKey()).publicKey }, headers));
      expect(ok.status).toBe(201);
      for (const name of ["my\nnode", "node_abc", "NODE_abc", "-dash-first", "x".repeat(65), "tab\tbed"]) {
        const res = await fetcher("/api/nodes", json({ name, publicKey: (await generateNodeKey()).publicKey }, cookieHeaders));
        expect(res.status, JSON.stringify(name)).toBe(400);
        expect(((await res.json()) as { error: string }).error).toBe("invalid_body");
      }
    });

    it("refuses a malformed body with 400 invalid_body", async () => {
      const { headers } = await newSession("eve");
      const res = await fetcher("/api/nodes", json({ name: "" }, headers));
      expect(res.status).toBe(400);
      expect(((await res.json()) as { error: string }).error).toBe("invalid_body");
    });
  });

  describe("the device-flow session", () => {
    it("is revoked once the node is registered, the browser's session is not", async () => {
      const { headers, cookieHeaders } = await newSession("ida");
      const created = await fetcher("/api/nodes", json({ name: "paired", publicKey: (await generateNodeKey()).publicKey }, headers));
      expect(created.status).toBe(201);
      const session = await fetcher("/api/auth/get-session", { headers });
      expect(session.status).toBe(200);
      expect(await session.json()).toBeNull();
      const again = await fetcher("/api/nodes", json({ name: "paired-again", publicKey: (await generateNodeKey()).publicKey }, headers));
      expect(again.status).toBe(401);
      expect(await listNodes(cookieHeaders)).toHaveLength(1);
    });

    it("survives a refused registration, so the agent can retry", async () => {
      const { headers } = await newSession("ivy");
      expect((await fetcher("/api/nodes", json({ name: "", publicKey: (await generateNodeKey()).publicKey }, headers))).status).toBe(400);
      expect(await (await fetcher("/api/auth/get-session", { headers })).json()).not.toBeNull();
      expect((await fetcher("/api/nodes", json({ name: "retry", publicKey: (await generateNodeKey()).publicKey }, headers))).status).toBe(201);
    });
  });

  describe("cross-site protection of POST /api/nodes", () => {
    const body = async () => ({ name: "csrf", publicKey: (await generateNodeKey()).publicKey });

    it("requires a JSON content type: a text/plain body that happens to parse is refused with 415", async () => {
      const { cookieHeaders } = await newSession("frank");
      const res = await fetcher("/api/nodes", {
        method: "POST",
        headers: { "content-type": "text/plain", ...cookieHeaders },
        body: JSON.stringify(await body()),
      });
      expect(res.status).toBe(415);
      expect(((await res.json()) as { error: string }).error).toBe("unsupported_media_type");
    });

    it("refuses a cookie session whose request comes from another origin with 403", async () => {
      const { cookieHeaders } = await newSession("grace");
      const foreign = await fetcher("/api/nodes", json(await body(), { ...cookieHeaders, origin: "https://evil.example" }));
      expect(foreign.status).toBe(403);
      expect(((await foreign.json()) as { error: string }).error).toBe("cross_site");
      const crossSite = await fetcher("/api/nodes", json(await body(), { ...cookieHeaders, "sec-fetch-site": "cross-site" }));
      expect(crossSite.status).toBe(403);
    });

    it("accepts a cookie session from its own origin, and a bearer session with no Origin at all", async () => {
      const { headers, cookieHeaders } = await newSession("hank");
      const sameOrigin = await fetcher("/api/nodes", json(await body(), { ...cookieHeaders, origin: new URL(baseURL).origin, "sec-fetch-site": "same-origin" }));
      expect(sameOrigin.status).toBe(201);
      const terminal = await fetcher("/api/nodes", json({ ...(await body()), name: "csrf-2" }, headers));
      expect(terminal.status).toBe(201);
    });
  });

  describe("signed unregistration", () => {
    async function registered(label: string) {
      const { headers, cookieHeaders } = await newSession(label);
      const key = await generateNodeKey();
      const res = await fetcher("/api/nodes", json({ name: label, publicKey: key.publicKey }, headers));
      expect(res.status).toBe(201);
      const { nodeId } = (await res.json()) as { nodeId: string };
      return { nodeId, key, cookieHeaders };
    }

    it("removes the node when the signature over the fresh timestamp verifies", async () => {
      const { nodeId, key, cookieHeaders } = await registered("heidi");
      const timestamp = new Date().toISOString();
      const signature = await key.sign(unregisterMessage(nodeId, timestamp));
      const res = await fetcher("/api/nodes/unregister", json({ nodeId, timestamp, signature }));
      expect(res.status).toBe(204);
      expect(await listNodes(cookieHeaders)).toHaveLength(0);
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

  describe("/connect", () => {
    it("requires a WebSocket upgrade", async () => {
      expect((await fetcher("/connect?node=node_x")).status).toBe(426);
    });

    it("answers 404 for an unknown node", async () => {
      const res = await fetcher("/connect?node=node_unknown", { headers: { Upgrade: "websocket" } });
      expect(res.status).toBe(404);
      expect(await res.text()).toBe("unknown node");
    });

    it("answers 400 without a node id", async () => {
      expect((await fetcher("/connect", { headers: { Upgrade: "websocket" } })).status).toBe(400);
    });
  });

  describe("the live channel, GET /api/nodes/live", () => {
    const upgrade = { Upgrade: "websocket", origin: new URL(baseURL).origin };

    it("requires a WebSocket upgrade", async () => {
      const { cookieHeaders } = await newSession("live-plain");
      const res = await fetcher("/api/nodes/live", { headers: { ...cookieHeaders, origin: new URL(baseURL).origin } });
      expect(res.status).toBe(426);
    });

    it("refuses another origin, and a request with no origin at all, with 403", async () => {
      const { cookieHeaders } = await newSession("live-cross");
      const foreign = await fetcher("/api/nodes/live", { headers: { ...cookieHeaders, ...upgrade, origin: "https://attacker.example" } });
      expect(foreign.status).toBe(403);
      const bare = await fetcher("/api/nodes/live", { headers: { ...cookieHeaders, Upgrade: "websocket" } });
      expect(bare.status).toBe(403);
    });

    it("refuses an anonymous page with 401", async () => {
      const res = await fetcher("/api/nodes/live", { headers: upgrade });
      expect(res.status).toBe(401);
    });
  });

  describe("JSON errors for machine clients", () => {
    it("answers 404 as JSON under /api and /mcp", async () => {
      for (const path of ["/api/nothing", "/mcp/nothing"]) {
        const res = await fetcher(path);
        expect(res.status, path).toBe(404);
        expect(res.headers.get("content-type"), path).toContain("application/json");
        expect(((await res.json()) as { error: string }).error).toBe("not_found");
      }
    });
  });
}
