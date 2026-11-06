import { z } from "zod";
import { protocolErrorSchema } from "./errors";

export const PROTOCOL_VERSION = 1 as const;
const version = z.literal(PROTOCOL_VERSION);

/** WebSocket close codes of PROTOCOL.md, "Transport". */
export const closeCodes = {
  protocolError: 4400,
  authFailed: 4401,
  unknownNode: 4404,
  replaced: 4409,
  tooManyAttempts: 4429,
} as const;

/** The node must authenticate within this many milliseconds of opening the socket. */
export const HANDSHAKE_TIMEOUT_MS = 10_000;
/** Interval the relay asks the node to ping at. */
export const PING_SECONDS = 30;
/** The relay waits at most this long for a command result. */
export const COMMAND_TIMEOUT_MS = 30_000;

// Node → relay

export const harnessSchema = z.object({
  id: z.string(),
  available: z.boolean(),
  version: z.string().optional(),
});
export type Harness = z.infer<typeof harnessSchema>;

export const platformSchema = z.object({
  os: z.string().optional(),
  arch: z.string().optional(),
  hostname: z.string().optional(),
});

export const helloSchema = z.object({
  v: version,
  type: z.literal("hello"),
  nodeId: z.string().min(1),
  publicKey: z.string().min(1),
  name: z.string(),
  agentVersion: z.string(),
  platform: platformSchema.optional(),
  harnesses: z.array(harnessSchema).default([]),
});
export type HelloMessage = z.infer<typeof helloSchema>;

export const authSchema = z.object({
  v: version,
  type: z.literal("auth"),
  signature: z.string().min(1),
});
export type AuthMessage = z.infer<typeof authSchema>;

export const pingSchema = z.object({ v: version, type: z.literal("ping") });

export const resultSchema = z.discriminatedUnion("ok", [
  z.object({ v: version, type: z.literal("result"), id: z.string(), ok: z.literal(true), data: z.unknown() }),
  z.object({ v: version, type: z.literal("result"), id: z.string(), ok: z.literal(false), error: protocolErrorSchema }),
]);
export type ResultMessage = z.infer<typeof resultSchema>;

/** Everything a node may send; unknown `type` values are not an error for the relay, which ignores them. */
export const nodeMessageSchema = z.discriminatedUnion("type", [helloSchema, authSchema, pingSchema, resultSchema]);
export type NodeMessage = z.infer<typeof nodeMessageSchema>;

/** The `type` of any frame, read before full validation so unknown types can be skipped. */
export const envelopeSchema = z.object({ v: version, type: z.string() });

// Relay → node

export const commandNames = ["teleport", "get_teleport_status", "approve_repository", "list_jobs", "ping"] as const;
export type CommandName = (typeof commandNames)[number];

export const commandSchema = z.object({
  v: version,
  type: z.literal("command"),
  id: z.string().min(1),
  name: z.enum(commandNames),
  params: z.record(z.string(), z.unknown()),
});
export type CommandMessage = z.infer<typeof commandSchema>;

export const challenge = (nonce: string) => ({ v: PROTOCOL_VERSION, type: "challenge", nonce }) as const;

export const welcome = (accountId: string, nodeId: string, pingSeconds: number) =>
  ({ v: PROTOCOL_VERSION, type: "welcome", accountId, nodeId, pingSeconds }) as const;

export const command = (id: string, name: CommandName, params: Record<string, unknown>): CommandMessage => ({
  v: PROTOCOL_VERSION,
  type: "command",
  id,
  name,
  params,
});

/** Sent before a fatal close during the handshake. */
export const errorFrame = (code: string, message: string) =>
  ({ v: PROTOCOL_VERSION, type: "error", code, message }) as const;

export const pong = () => ({ v: PROTOCOL_VERSION, type: "pong" }) as const;

/** The exact string the node signs with its Ed25519 key (PROTOCOL.md, "Handshake"). */
export const authMessage = (nodeId: string, nonce: string): string => `wrekin-node-auth-v1\n${nodeId}\n${nonce}`;
