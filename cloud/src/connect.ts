import type { Context } from "hono";
import type { AppEnv } from "./app";
import { servicesFor } from "./services";

/**
 * GET /connect?node=<id>: the node's WebSocket. The Worker only checks that the node
 * exists and hands the upgrade to the account's hub, which runs the handshake.
 */
export async function connect(c: Context<AppEnv>): Promise<Response> {
  if (c.req.header("Upgrade")?.toLowerCase() !== "websocket") {
    return c.text("expected a WebSocket upgrade", 426);
  }
  const nodeId = c.req.query("node");
  if (!nodeId) return c.text("missing node", 400);
  const { nodes, hub } = servicesFor(c.env);
  const node = await nodes.findNodeById(nodeId);
  // Plain 404: the agent then waits the back-off cap, since only pairing again can fix this.
  if (!node) return c.text("unknown node", 404);
  return hub(node.accountId).connect(c.req.raw, node);
}
