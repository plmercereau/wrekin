import { env, SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { createAuth } from "../src/auth";
import { authMessage } from "../src/protocol";
import { generateNodeKey } from "./helpers/keys";
import { createTestSession } from "./helpers/session";
import { listNodesOverHttp } from "./helpers/trpc-http";

// The full path of a node: registered through the API, then connected through the Worker's
// /connect route, which forwards the upgrade to the account's hub.
describe("GET /connect through the Worker", () => {
  it("upgrades a registered node and runs the handshake", async () => {
    // The bearer session is revoked once the node is registered; the list is read with the cookie one.
    const { headers, cookieHeaders } = await createTestSession(createAuth(env), "connect-user");
    const key = await generateNodeKey();
    const created = await SELF.fetch(`${env.BASE_URL}/api/nodes`, {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ name: "ws", publicKey: key.publicKey }),
    });
    expect(created.status).toBe(201);
    const { nodeId } = (await created.json()) as { nodeId: string };

    const res = await SELF.fetch(`${env.BASE_URL}/connect?node=${nodeId}`, { headers: { Upgrade: "websocket" } });
    expect(res.status).toBe(101);
    const ws = res.webSocket;
    if (!ws) throw new Error("no WebSocket");
    ws.accept();
    const frames: Array<Record<string, unknown>> = [];
    const next = () =>
      new Promise<Record<string, unknown>>((resolve, reject) => {
        const buffered = frames.shift();
        if (buffered) return resolve(buffered);
        const timer = setTimeout(() => reject(new Error("no frame within 2 s")), 2000);
        ws.addEventListener(
          "message",
          (event) => {
            clearTimeout(timer);
            resolve(JSON.parse(String(event.data)) as Record<string, unknown>);
          },
          { once: true },
        );
      });
    ws.send(JSON.stringify({ v: 1, type: "hello", nodeId, publicKey: key.publicKey, name: "ws", agentVersion: "0.1.0", harnesses: [] }));
    const challenge = await next();
    expect(challenge.type).toBe("challenge");
    ws.send(JSON.stringify({ v: 1, type: "auth", signature: await key.sign(authMessage(nodeId, challenge.nonce as string)) }));
    const welcome = await next();
    expect(welcome).toMatchObject({ type: "welcome", nodeId, pingSeconds: 30 });

    const nodes = await listNodesOverHttp((path, init) => SELF.fetch(`${env.BASE_URL}${path}`, init))(cookieHeaders);
    expect(nodes.find((n) => n.id === nodeId)?.online).toBe(true);
    ws.close(1000, "done");
  });
});
