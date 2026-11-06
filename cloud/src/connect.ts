import type { Env } from "./env";
import { servicesFor } from "./services";

/**
 * GET /connect?node=<id>: the node's WebSocket. The Worker only checks that the node
 * exists and hands the upgrade to the account's hub, which runs the handshake.
 */
export async function connect(request: Request, env: Env): Promise<Response> {
  if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
    return new Response("expected a WebSocket upgrade", { status: 426 });
  }
  const nodeId = new URL(request.url).searchParams.get("node");
  if (!nodeId) return new Response("missing node", { status: 400 });
  const { nodes, hub } = servicesFor(env);
  const node = await nodes.findNodeById(nodeId);
  // Plain 404: the agent then waits the back-off cap, since only pairing again can fix this.
  if (!node) return new Response("unknown node", { status: 404 });
  return hub(node.accountId).connect(request, node);
}
