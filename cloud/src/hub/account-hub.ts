import { DurableObject } from "cloudflare:workers";
import { z } from "zod";
import { encodeBase64, verifyEd25519 } from "../crypto";
import { getDb } from "../db/kysely";
import { KyselyNodeStore } from "../db/nodes";
import type { Env } from "../env";
import { commandNames } from "../protocol";
import { HubCore, type Attachment, type HubSocket } from "./core";
import { ACCOUNT_ID_HEADER, PUBLIC_KEY_HEADER } from "./headers";

export { ACCOUNT_ID_HEADER, PUBLIC_KEY_HEADER };


const commandRequestSchema = z.object({
  nodeId: z.string().min(1),
  name: z.enum(commandNames),
  params: z.record(z.string(), z.unknown()).default({}),
  timeoutMs: z.number().int().positive().max(60_000).optional(),
});

const wrap = (ws: WebSocket): HubSocket => ({
  send: (data) => ws.send(data),
  close: (code, reason) => ws.close(code, reason),
  getAttachment: () => (ws.deserializeAttachment() as Attachment | null) ?? null,
  setAttachment: (attachment) => ws.serializeAttachment(attachment),
});

/**
 * One instance per account. Owns the node WebSockets (Hibernation API) and routes
 * commands to them; the logic lives in HubCore, this class adapts Cloudflare's APIs.
 */
export class AccountHub extends DurableObject<Env> {
  private readonly core: HubCore;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    // Liveness pings are answered by the runtime without waking the object.
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair('{"v":1,"type":"ping"}', '{"v":1,"type":"pong"}'));
    this.core = new HubCore({
      sockets: () => ctx.getWebSockets().map(wrap),
      verify: verifyEd25519,
      randomNonce: () => encodeBase64(crypto.getRandomValues(new Uint8Array(32))),
      now: () => new Date(),
      onAuthenticated: (nodeId, at) => new KyselyNodeStore(getDb(env)).touchNode(nodeId, at),
    });
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/connect") {
      if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
        return new Response("expected a WebSocket upgrade", { status: 426 });
      }
      const nodeId = url.searchParams.get("nodeId");
      const publicKey = request.headers.get(PUBLIC_KEY_HEADER);
      const accountId = request.headers.get(ACCOUNT_ID_HEADER);
      if (!nodeId || !publicKey || !accountId) return new Response("missing node identity", { status: 400 });
      const pair = new WebSocketPair();
      const [client, server] = [pair[0], pair[1]];
      this.ctx.acceptWebSocket(server, [nodeId]);
      this.core.onOpen(wrap(server), { accountId, nodeId, publicKey });
      return new Response(null, { status: 101, webSocket: client });
    }

    if (url.pathname === "/nodes/online" && request.method === "GET") {
      return Response.json({ nodes: this.core.online() });
    }

    if (url.pathname === "/command" && request.method === "POST") {
      const body = commandRequestSchema.safeParse(await request.json().catch(() => null));
      if (!body.success) return Response.json({ error: "invalid_body", message: z.prettifyError(body.error) }, { status: 400 });
      const { nodeId, name, params, timeoutMs } = body.data;
      return Response.json(await this.core.command(nodeId, name, params, timeoutMs));
    }

    return new Response("not found", { status: 404 });
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    await this.core.onMessage(wrap(ws), message);
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string, _wasClean: boolean): Promise<void> {
    this.core.onClose(wrap(ws), code, reason);
  }

  async webSocketError(ws: WebSocket, error: unknown): Promise<void> {
    console.warn("hub: socket error", error instanceof Error ? error.message : String(error));
    this.core.onClose(wrap(ws), 1006, "error");
  }
}
