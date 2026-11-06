import { z } from "zod";
import { canonicalPublicKey, fingerprint, verifyEd25519 } from "../crypto";
import { NodeConflictError, type NodeRecord, type NodeStore } from "../db/nodes";
import type { HubFactory, OnlineNode } from "../hub/client";

/** The string a node signs to unregister itself (see `wrekin logout`). */
export const unregisterMessage = (nodeId: string, timestamp: string): string =>
  `wrekin-node-unregister-v1\n${nodeId}\n${timestamp}`;

/** An unregistration timestamp is accepted within this many seconds of the Worker's clock. */
export const UNREGISTER_SKEW_SECONDS = 300;

/**
 * A node name: a letter or digit, then up to 63 letters, digits, spaces, dots, underscores
 * or dashes. No control character reaches the pages or the MCP text, and nothing shaped
 * like a node id (`node_…`) can make `findNode(accountId, idOrName)` ambiguous.
 */
export const NODE_NAME = /^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,63}$/u;

export const registerSchema = z.object({
  name: z
    .string()
    .trim()
    .regex(NODE_NAME, "a name is 1 to 64 letters, digits, spaces, dots, underscores or dashes, starting with a letter or digit")
    .refine((name) => !/^node_/i.test(name), "a name must not start with node_, the shape of a node id"),
  publicKey: z.string().min(1),
  agentVersion: z.string().max(64).optional(),
  platform: z.string().max(128).optional(),
});
export type RegisterInput = z.infer<typeof registerSchema>;

export const unregisterSchema = z.object({
  nodeId: z.string().min(1),
  /** RFC 3339 or Unix seconds; signed verbatim as a string. */
  timestamp: z.union([z.string().min(1), z.number().int()]),
  signature: z.string().min(1),
});
export type UnregisterInput = z.infer<typeof unregisterSchema>;

/** A node as `nodes.list` and the pages present it: never the public key itself, only its fingerprint. */
export type NodeView = {
  id: string;
  name: string;
  fingerprint: string;
  online: boolean;
  connectedAt: string | null;
  lastSeenAt: string | null;
  agentVersion: string | null;
  platform: string | null;
  harnesses: NonNullable<OnlineNode["harnesses"]>;
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

/**
 * A refusal the plain HTTP route and the tRPC procedure both translate: `status` and `code`
 * are those of the REST API the Go agent speaks (409 name_taken, 400 invalid_public_key, …).
 */
export class NodeServiceError extends Error {
  constructor(
    readonly status: 400 | 401 | 404 | 409,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "NodeServiceError";
  }
}

/** What the node operations need: the store and the account's hub. `Services` satisfies it. */
export type NodeServices = { nodes: NodeStore; hub: HubFactory };

/** The account's nodes merged with the hub's online state, oldest first. */
export async function listNodes({ nodes, hub }: NodeServices, accountId: string): Promise<NodeView[]> {
  const [rows, online] = await Promise.all([nodes.listNodes(accountId), hub(accountId).online()]);
  return Promise.all(rows.map((row) => toNodeView(row, online)));
}

/** Registers a node for `accountId` (what `wrekin auth` does once the device code is approved). */
export async function registerNode(
  { nodes, hub }: NodeServices,
  accountId: string,
  input: RegisterInput,
): Promise<{ nodeId: string; accountId: string }> {
  // Stored in canonical base64, so the UNIQUE check and the hub's comparison see one spelling per key.
  const publicKey = canonicalPublicKey(input.publicKey);
  if (!publicKey) {
    throw new NodeServiceError(400, "invalid_public_key", "publicKey must be the base64 of 32 raw Ed25519 bytes");
  }
  try {
    const node = await nodes.insertNode({ accountId, ...input, publicKey });
    await announce(hub, accountId);
    return { nodeId: node.id, accountId: node.accountId };
  } catch (error) {
    if (error instanceof NodeConflictError) {
      throw new NodeServiceError(409, error.field === "name" ? "name_taken" : "key_taken", error.message);
    }
    throw error;
  }
}

/** Tells the account's open nodes pages about a new row. Best effort: a page that misses it catches up on its next load. */
async function announce(hub: HubFactory, accountId: string): Promise<void> {
  try {
    await hub(accountId).changed();
  } catch (error) {
    console.warn("nodes: could not tell the hub about a registration", error instanceof Error ? error.message : String(error));
  }
}

/** Removes one of the account's nodes and closes its live socket; false when the account has no such node. */
export async function removeNode({ nodes, hub }: NodeServices, accountId: string, id: string): Promise<boolean> {
  const removed = await nodes.deleteNode(accountId, id);
  if (removed) await evict(hub, accountId, id);
  return removed;
}

/**
 * Closes the removed node's socket (4404). Best effort once the row is gone: should the hub
 * not answer, the node is refused with 404 at its next reconnect anyway.
 */
async function evict(hub: HubFactory, accountId: string, nodeId: string): Promise<void> {
  try {
    await hub(accountId).evict(nodeId);
  } catch (error) {
    console.warn(`nodes: could not evict ${nodeId} from the hub`, error instanceof Error ? error.message : String(error));
  }
}

/** A node removes itself with its key, no session involved (`wrekin logout` on a headless box). */
export async function unregisterNode({ nodes, hub }: NodeServices, input: UnregisterInput): Promise<void> {
  const { nodeId, timestamp, signature } = input;
  const at = parseTimestamp(timestamp);
  if (at === null || Math.abs(Date.now() - at) > UNREGISTER_SKEW_SECONDS * 1000) {
    throw new NodeServiceError(401, "stale_timestamp", `timestamp must be within ${UNREGISTER_SKEW_SECONDS} s`);
  }
  const node = await nodes.findNodeById(nodeId);
  if (!node) throw new NodeServiceError(404, "unknown_node", "no node with that id");
  const valid = await verifyEd25519(node.publicKey, unregisterMessage(nodeId, String(timestamp)), signature);
  if (!valid) throw new NodeServiceError(401, "bad_signature", "the signature does not verify against the node's key");
  if (await nodes.deleteNode(node.accountId, node.id)) await evict(hub, node.accountId, node.id);
}
