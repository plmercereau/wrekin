import { Hono } from "hono";
import { z } from "zod";
import type { AppEnv } from "../app";
import { fingerprint, parsePublicKey, verifyEd25519 } from "../crypto";
import { NodeConflictError, type NodeRecord } from "../db/nodes";
import type { OnlineNode } from "../hub/client";
import { servicesFor } from "../services";

/** The string a node signs to unregister itself (see `wrekin logout`). */
export const unregisterMessage = (nodeId: string, timestamp: string): string =>
  `wrekin-node-unregister-v1\n${nodeId}\n${timestamp}`;

/** An unregistration timestamp is accepted within this many seconds of the Worker's clock. */
export const UNREGISTER_SKEW_SECONDS = 300;

const registerSchema = z.object({
  name: z.string().trim().min(1).max(64),
  publicKey: z.string().min(1),
  agentVersion: z.string().max(64).optional(),
  platform: z.string().max(128).optional(),
});

const unregisterSchema = z.object({
  nodeId: z.string().min(1),
  /** RFC 3339 or Unix seconds; signed verbatim as a string. */
  timestamp: z.union([z.string().min(1), z.number().int()]),
  signature: z.string().min(1),
});

/** A node as GET /api/nodes and the pages present it. */
export type NodeView = {
  id: string;
  name: string;
  fingerprint: string;
  online: boolean;
  connectedAt: string | null;
  lastSeenAt: string | null;
  agentVersion: string | null;
  platform: string | null;
  harnesses: OnlineNode["harnesses"];
  createdAt: string;
};

export async function toNodeView(node: NodeRecord, online: OnlineNode[]): Promise<NodeView> {
  const live = online.find((n) => n.nodeId === node.id);
  return {
    id: node.id,
    name: node.name,
    fingerprint: await fingerprint(node.publicKey),
    online: live !== undefined,
    connectedAt: live?.connectedAt ?? null,
    lastSeenAt: node.lastSeenAt,
    agentVersion: live?.agentVersion ?? node.agentVersion,
    platform: node.platform,
    harnesses: live?.harnesses ?? [],
    createdAt: node.createdAt,
  };
}

/** Parses an RFC 3339 string or Unix seconds into milliseconds since the epoch, or null. */
export function parseTimestamp(value: string | number): number | null {
  if (typeof value === "number") return Number.isFinite(value) ? value * 1000 : null;
  if (/^\d+$/.test(value)) return Number(value) * 1000;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
}

export function nodesApi() {
  const api = new Hono<AppEnv>();

  // Authenticated routes: the session comes from the cookie or `Authorization: Bearer`.
  const withSession = async (headers: Headers, env: AppEnv["Bindings"]) => {
    const { auth } = servicesFor(env);
    const session = await auth.api.getSession({ headers });
    return session?.user ?? null;
  };

  api.post("/", async (c) => {
    const user = await withSession(c.req.raw.headers, c.env);
    if (!user) return c.json({ error: "unauthenticated" }, 401);
    const body = registerSchema.safeParse(await c.req.json().catch(() => null));
    if (!body.success) return c.json({ error: "invalid_body", message: z.prettifyError(body.error) }, 400);
    if (!parsePublicKey(body.data.publicKey)) {
      return c.json({ error: "invalid_public_key", message: "publicKey must be the base64 of 32 raw Ed25519 bytes" }, 400);
    }
    const { nodes } = servicesFor(c.env);
    try {
      const node = await nodes.insertNode({ accountId: user.id, ...body.data });
      return c.json({ nodeId: node.id, accountId: node.accountId }, 201);
    } catch (error) {
      if (error instanceof NodeConflictError) {
        return c.json({ error: error.field === "name" ? "name_taken" : "key_taken", message: error.message }, 409);
      }
      throw error;
    }
  });

  api.get("/", async (c) => {
    const user = await withSession(c.req.raw.headers, c.env);
    if (!user) return c.json({ error: "unauthenticated" }, 401);
    const { nodes, hub } = servicesFor(c.env);
    const [rows, online] = await Promise.all([nodes.listNodes(user.id), hub(user.id).online()]);
    return c.json({ nodes: await Promise.all(rows.map((row) => toNodeView(row, online))) });
  });

  api.delete("/:id", async (c) => {
    const user = await withSession(c.req.raw.headers, c.env);
    if (!user) return c.json({ error: "unauthenticated" }, 401);
    const deleted = await servicesFor(c.env).nodes.deleteNode(user.id, c.req.param("id"));
    return deleted ? c.body(null, 204) : c.json({ error: "unknown_node" }, 404);
  });

  // A node removes itself with its key, no session involved (`wrekin logout` on a headless box).
  api.post("/unregister", async (c) => {
    const body = unregisterSchema.safeParse(await c.req.json().catch(() => null));
    if (!body.success) return c.json({ error: "invalid_body", message: z.prettifyError(body.error) }, 400);
    const { nodeId, timestamp, signature } = body.data;
    const at = parseTimestamp(timestamp);
    if (at === null || Math.abs(Date.now() - at) > UNREGISTER_SKEW_SECONDS * 1000) {
      return c.json({ error: "stale_timestamp", message: `timestamp must be within ${UNREGISTER_SKEW_SECONDS} s` }, 401);
    }
    const { nodes } = servicesFor(c.env);
    const node = await nodes.findNodeById(nodeId);
    if (!node) return c.json({ error: "unknown_node" }, 404);
    const valid = await verifyEd25519(node.publicKey, unregisterMessage(nodeId, String(timestamp)), signature);
    if (!valid) return c.json({ error: "bad_signature" }, 401);
    await nodes.deleteNode(node.accountId, node.id);
    return c.body(null, 204);
  });

  return api;
}
