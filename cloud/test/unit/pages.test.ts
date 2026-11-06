import { describe, expect, it } from "vitest";
import { FakeHub } from "../helpers/fake-hub";
import { generateNodeKey } from "../helpers/keys";
import { MemoryNodeStore } from "../helpers/memory-nodes";
import { nodeApp, TEST_BASE_URL } from "../helpers/node-app";
import { createTestSession } from "../helpers/session";
import { pagesSuite } from "../shared/pages-suite";

const hub = new FakeHub();
const { fetcher, services } = nodeApp({ nodes: new MemoryNodeStore(), hub: hub.factory });

pagesSuite(fetcher, TEST_BASE_URL, (label) => createTestSession(services.auth, label));

describe("nodes page with the hub", () => {
  it("shows the online badge and the harnesses of a connected node", async () => {
    const { userId, headers } = await createTestSession(services.auth, "online-page");
    const key = await generateNodeKey();
    const created = await fetcher("/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ name: "studio", publicKey: key.publicKey }),
    });
    const { nodeId } = (await created.json()) as { nodeId: string };
    hub.setOnline(userId, [{ nodeId, connectedAt: "2026-11-06T00:00:00Z", agentVersion: "0.1.0", harnesses: [{ id: "claude-code", available: true, version: "2.1.0" }] }]);
    const body = await (await fetcher("/nodes", { headers })).text();
    expect(body).toContain('class="badge online"');
    expect(body).toContain("claude-code 2.1.0");
    expect(body).toContain("Connected since 2026-11-06 00:00:00 UTC");
  });

  it("escapes what comes from the database", async () => {
    const { headers } = await createTestSession(services.auth, "xss-page");
    const key = await generateNodeKey();
    await fetcher("/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ name: "<img src=x onerror=alert(1)>", publicKey: key.publicKey }),
    });
    const body = await (await fetcher("/nodes", { headers })).text();
    expect(body).not.toContain("<img src=x");
    expect(body).toContain("&lt;img src=x");
  });
});
