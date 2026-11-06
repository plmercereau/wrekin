import { describe, expect, it } from "vitest";
import { generateNodeKey } from "../helpers/keys";
import type { Fetcher } from "./auth-suite";
import type { SessionFactory } from "./nodes-suite";

/** Assertions shared by the plain-Node and the workerd runs of the pages and the connect route. */
export function pagesSuite(fetcher: Fetcher, baseURL: string, newSession: SessionFactory) {
  describe("pages", () => {
    it("explains how to connect Claude on the front page", async () => {
      const res = await fetcher("/");
      expect(res.status).toBe(200);
      expect(res.headers.get("content-type")).toContain("text/html");
      const body = await res.text();
      expect(body).toContain(`${baseURL}/mcp`);
      expect(body).toContain("brew install plmercereau/tap/wrekin");
      expect(body).toContain('<meta name="viewport"');
      expect(body).not.toMatch(/<script[^>]+src=/); // nothing loaded from a CDN
    });

    it("offers GitHub and Google on the login page", async () => {
      const res = await fetcher("/login?redirect=/nodes");
      expect(res.status).toBe(200);
      const body = await res.text();
      expect(body).toContain('data-provider="github"');
      expect(body).toContain('data-provider="google"');
      expect(body).toContain("/api/auth/sign-in/social");
    });

    it("sends an anonymous visitor of /device to the login page and back", async () => {
      const res = await fetcher("/device?user_code=ABCD-EFGH", { redirect: "manual" });
      expect(res.status).toBe(302);
      expect(res.headers.get("location")).toBe("/login?redirect=%2Fdevice%3Fuser_code%3DABCD-EFGH");
    });

    it("sends an anonymous visitor of /nodes and /consent to the login page", async () => {
      const nodes = await fetcher("/nodes", { redirect: "manual" });
      expect(nodes.status).toBe(302);
      expect(nodes.headers.get("location")).toBe("/login?redirect=%2Fnodes");
      const consent = await fetcher("/consent?client_id=abc&scope=openid", { redirect: "manual" });
      expect(consent.status).toBe(302);
      expect(consent.headers.get("location")).toContain("/login?redirect=%2Fconsent");
    });

    it("shows a pending device code to a signed-in user with Approve and Deny", async () => {
      const { headers } = await newSession("device-user");
      const issued = await fetcher("/api/auth/device/code", {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ client_id: "wrekin-agent" }),
      });
      const { user_code } = (await issued.json()) as { user_code: string };
      const res = await fetcher(`/device?user_code=${user_code.slice(0, 4)}-${user_code.slice(4).toLowerCase()}`, { headers });
      expect(res.status).toBe(200);
      const body = await res.text();
      expect(body).toContain(user_code);
      expect(body).toContain('id="approve"');
      expect(body).toContain('id="deny"');
      expect(body).toContain("Wrekin agent");
    });

    it("tells a signed-in user when a device code is unknown", async () => {
      const { headers } = await newSession("device-user-2");
      const res = await fetcher("/device?user_code=NOPE0000", { headers });
      expect(res.status).toBe(200);
      expect(await res.text()).toContain("not known or has expired");
    });

    it("lists the signed-in user's nodes with fingerprint and state", async () => {
      const { headers } = await newSession("nodes-user");
      const key = await generateNodeKey();
      await fetcher("/api/nodes", {
        method: "POST",
        headers: { "content-type": "application/json", ...headers },
        body: JSON.stringify({ name: "kitchen-mac", publicKey: key.publicKey }),
      });
      const res = await fetcher("/nodes", { headers });
      expect(res.status).toBe(200);
      const body = await res.text();
      expect(body).toContain("kitchen-mac");
      expect(body).toMatch(/[0-9a-f]{4}(:[0-9a-f]{4}){3}/);
      expect(body).toContain("offline");
      expect(body).toContain('data-remove="node_');
    });

    it("answers 404 as JSON under /api and as HTML elsewhere", async () => {
      const api = await fetcher("/api/nothing");
      expect(api.status).toBe(404);
      expect(api.headers.get("content-type")).toContain("application/json");
      const page = await fetcher("/nothing");
      expect(page.status).toBe(404);
      expect(page.headers.get("content-type")).toContain("text/html");
    });
  });

  describe("/connect", () => {
    it("requires a WebSocket upgrade", async () => {
      const res = await fetcher("/connect?node=node_x");
      expect(res.status).toBe(426);
    });

    it("answers 404 for an unknown node", async () => {
      const res = await fetcher("/connect?node=node_unknown", { headers: { Upgrade: "websocket" } });
      expect(res.status).toBe(404);
      expect(await res.text()).toBe("unknown node");
    });

    it("answers 400 without a node id", async () => {
      const res = await fetcher("/connect", { headers: { Upgrade: "websocket" } });
      expect(res.status).toBe(400);
    });
  });
}
