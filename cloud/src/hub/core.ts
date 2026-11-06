import { canonicalPublicKey, randomId } from "../crypto";
import {
  authMessage,
  authSchema,
  challenge,
  closeCodes,
  command,
  COMMAND_TIMEOUT_MS,
  envelopeSchema,
  errorFrame,
  HANDSHAKE_TIMEOUT_MS,
  helloSchema,
  PING_SECONDS,
  pong,
  resultSchema,
  welcome,
  type CommandName,
  type Harness,
} from "../protocol";
import type { OnlineNode, Result } from "./client";

/** What the hub remembers about one socket; survives hibernation as the WebSocket attachment. */
export type Attachment = {
  connectionId: string;
  accountId: string;
  nodeId: string;
  /** The key registered at pairing time, read from D1 by the Worker. */
  publicKey: string;
  stage: "hello" | "challenged" | "authed";
  nonce?: string;
  connectedAt: string;
  name?: string;
  agentVersion?: string;
  harnesses?: Harness[];
};

/** The slice of a WebSocket the core needs; the Durable Object adapts the real one, tests fake it. */
export interface HubSocket {
  send(data: string): void;
  close(code: number, reason: string): void;
  getAttachment(): Attachment | null;
  setAttachment(attachment: Attachment): void;
}

export interface HubHost {
  /** Every open socket of this account, hibernated ones included. */
  sockets(): HubSocket[];
  verify(publicKey: string, message: string, signature: string): Promise<boolean>;
  /** Base64 of 32 random bytes. */
  randomNonce(): string;
  now(): Date;
  /** Called once a node has authenticated: the Worker records `last_seen_at`. */
  onAuthenticated(nodeId: string, at: string): Promise<void>;
}

export type HubCoreOptions = {
  handshakeTimeoutMs?: number;
  pingSeconds?: number;
};

type Pending = {
  nodeId: string;
  connectionId: string;
  resolve: (result: Result<unknown>) => void;
  timer: ReturnType<typeof setTimeout>;
};

/**
 * The account hub's logic, free of Cloudflare APIs: the node handshake of
 * PROTOCOL.md, one live socket per node, command routing with timeouts.
 */
export class HubCore {
  private readonly pending = new Map<string, Pending>();
  private readonly handshakeTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private readonly handshakeTimeoutMs: number;
  private readonly pingSeconds: number;

  constructor(
    private readonly host: HubHost,
    options: HubCoreOptions = {},
  ) {
    this.handshakeTimeoutMs = options.handshakeTimeoutMs ?? HANDSHAKE_TIMEOUT_MS;
    this.pingSeconds = options.pingSeconds ?? PING_SECONDS;
  }

  /** A socket was accepted for `nodeId`; nothing is sent until the node says hello. */
  onOpen(socket: HubSocket, identity: { accountId: string; nodeId: string; publicKey: string }): void {
    const attachment: Attachment = {
      connectionId: randomId("conn"),
      accountId: identity.accountId,
      nodeId: identity.nodeId,
      publicKey: identity.publicKey,
      stage: "hello",
      connectedAt: this.host.now().toISOString(),
    };
    socket.setAttachment(attachment);
    const timer = setTimeout(() => {
      this.handshakeTimers.delete(attachment.connectionId);
      const live = this.socketOf(attachment.connectionId);
      if (live && live.getAttachment()?.stage !== "authed") {
        this.fail(live, closeCodes.protocolError, "protocol_error", "handshake timeout");
      }
    }, this.handshakeTimeoutMs);
    this.handshakeTimers.set(attachment.connectionId, timer);
  }

  async onMessage(socket: HubSocket, data: string | ArrayBuffer): Promise<void> {
    const attachment = socket.getAttachment();
    if (!attachment) return this.fail(socket, closeCodes.protocolError, "protocol_error", "unknown connection");
    if (typeof data !== "string") return this.fail(socket, closeCodes.protocolError, "protocol_error", "text frames only");
    let raw: unknown;
    try {
      raw = JSON.parse(data);
    } catch {
      return this.fail(socket, closeCodes.protocolError, "protocol_error", "malformed frame");
    }
    const envelope = envelopeSchema.safeParse(raw);
    if (!envelope.success) return this.fail(socket, closeCodes.protocolError, "protocol_error", "unsupported frame");
    switch (envelope.data.type) {
      case "hello":
        return this.onHello(socket, attachment, raw);
      case "auth":
        return this.onAuth(socket, attachment, raw);
      case "ping":
        return this.trySend(socket, JSON.stringify(pong()));
      case "result":
        return this.onResult(socket, attachment, raw);
      default:
        // Unknown types are ignored, for forward compatibility within the version.
        return;
    }
  }

  /** The socket is gone: the node is offline and its in-flight commands fail now. */
  onClose(socket: HubSocket, _code: number, _reason: string): void {
    const attachment = socket.getAttachment();
    if (!attachment) return;
    this.clearHandshakeTimer(attachment.connectionId);
    for (const [id, entry] of this.pending) {
      if (entry.connectionId !== attachment.connectionId) continue;
      clearTimeout(entry.timer);
      this.pending.delete(id);
      entry.resolve({ ok: false, error: { code: "node_offline", message: "the node disconnected before answering" } });
    }
  }

  /** Nodes with an authenticated socket, one entry per node. */
  online(): OnlineNode[] {
    const byNode = new Map<string, Attachment>();
    for (const attachment of this.authedAttachments()) {
      const seen = byNode.get(attachment.nodeId);
      if (!seen || seen.connectedAt < attachment.connectedAt) byNode.set(attachment.nodeId, attachment);
    }
    return [...byNode.values()].map((a) => ({
      nodeId: a.nodeId,
      connectedAt: a.connectedAt,
      ...(a.agentVersion !== undefined ? { agentVersion: a.agentVersion } : {}),
      ...(a.harnesses !== undefined ? { harnesses: a.harnesses } : {}),
    }));
  }

  /** Sends a command to `nodeId` and waits for its result, `node_offline` or `node_timeout`. */
  command(nodeId: string, name: CommandName, params: Record<string, unknown>, timeoutMs = COMMAND_TIMEOUT_MS): Promise<Result<unknown>> {
    const target = this.authedSocketOf(nodeId);
    if (!target) {
      return Promise.resolve({ ok: false, error: { code: "node_offline", message: "the node has no open connection" } });
    }
    const id = randomId("cmd");
    return new Promise<Result<unknown>>((resolve) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        resolve({ ok: false, error: { code: "node_timeout", message: `the node did not answer within ${Math.round(timeoutMs / 1000)} s` } });
      }, timeoutMs);
      this.pending.set(id, { nodeId, connectionId: target.attachment.connectionId, resolve, timer });
      try {
        target.socket.send(JSON.stringify(command(id, name, params)));
      } catch {
        clearTimeout(timer);
        this.pending.delete(id);
        resolve({ ok: false, error: { code: "node_offline", message: "the node's connection is closing" } });
      }
    });
  }

  private onHello(socket: HubSocket, attachment: Attachment, raw: unknown): void {
    if (attachment.stage !== "hello") return this.fail(socket, closeCodes.protocolError, "protocol_error", "hello out of order");
    const hello = helloSchema.safeParse(raw);
    if (!hello.success) return this.fail(socket, closeCodes.protocolError, "protocol_error", "invalid hello");
    if (hello.data.nodeId !== attachment.nodeId) {
      return this.fail(socket, closeCodes.protocolError, "protocol_error", "nodeId does not match the connection");
    }
    // Compared as key bytes, not as strings: either side may carry a non-canonical base64 spelling.
    const presented = canonicalPublicKey(hello.data.publicKey);
    if (presented === null || presented !== canonicalPublicKey(attachment.publicKey)) {
      return this.fail(socket, closeCodes.authFailed, "auth_failed", "public key does not match the registered one");
    }
    const nonce = this.host.randomNonce();
    socket.setAttachment({
      ...attachment,
      stage: "challenged",
      nonce,
      name: hello.data.name,
      agentVersion: hello.data.agentVersion,
      harnesses: hello.data.harnesses,
    });
    this.trySend(socket, JSON.stringify(challenge(nonce)));
  }

  private async onAuth(socket: HubSocket, attachment: Attachment, raw: unknown): Promise<void> {
    if (attachment.stage !== "challenged" || !attachment.nonce) {
      return this.fail(socket, closeCodes.protocolError, "protocol_error", "auth out of order");
    }
    const auth = authSchema.safeParse(raw);
    if (!auth.success) return this.fail(socket, closeCodes.protocolError, "protocol_error", "invalid auth");
    const valid = await this.host.verify(attachment.publicKey, authMessage(attachment.nodeId, attachment.nonce), auth.data.signature);
    if (!valid) return this.fail(socket, closeCodes.authFailed, "auth_failed", "bad signature");

    this.clearHandshakeTimer(attachment.connectionId);
    const { nonce: _nonce, ...rest } = attachment;
    socket.setAttachment({ ...rest, stage: "authed" });
    this.trySend(socket, JSON.stringify(welcome(attachment.accountId, attachment.nodeId, this.pingSeconds)));

    // One live connection per node: an older one is replaced.
    for (const other of this.host.sockets()) {
      const a = other.getAttachment();
      if (a && a.nodeId === attachment.nodeId && a.connectionId !== attachment.connectionId) {
        try {
          other.close(closeCodes.replaced, "replaced by a newer connection of the same node");
        } catch {
          // already gone
        }
      }
    }

    try {
      await this.host.onAuthenticated(attachment.nodeId, this.host.now().toISOString());
    } catch (error) {
      console.warn("hub: could not record last_seen_at", error instanceof Error ? error.message : String(error));
    }
  }

  private onResult(socket: HubSocket, attachment: Attachment, raw: unknown): void {
    if (attachment.stage !== "authed") return this.fail(socket, closeCodes.protocolError, "protocol_error", "result before auth");
    const result = resultSchema.safeParse(raw);
    if (!result.success) return this.fail(socket, closeCodes.protocolError, "protocol_error", "invalid result");
    const entry = this.pending.get(result.data.id);
    if (!entry) return; // late or duplicate answer
    if (entry.connectionId !== attachment.connectionId) {
      // Only the connection the command went to may answer it: another node of the
      // account, or an older socket of the same node, cannot resolve or forge a result.
      console.warn(
        `hub: result for ${result.data.id} from connection ${attachment.connectionId} (node ${attachment.nodeId}); ` +
          `the command went to connection ${entry.connectionId} (node ${entry.nodeId}); ignored`,
      );
      return;
    }
    clearTimeout(entry.timer);
    this.pending.delete(result.data.id);
    entry.resolve(result.data.ok ? { ok: true, data: result.data.data } : { ok: false, error: result.data.error });
  }

  private fail(socket: HubSocket, closeCode: number, code: string, message: string): void {
    this.trySend(socket, JSON.stringify(errorFrame(code, message)));
    try {
      socket.close(closeCode, message);
    } catch {
      // already closed
    }
  }

  private trySend(socket: HubSocket, data: string): void {
    try {
      socket.send(data);
    } catch {
      // the socket is closing; the close event cleans up
    }
  }

  private clearHandshakeTimer(connectionId: string): void {
    const timer = this.handshakeTimers.get(connectionId);
    if (timer !== undefined) {
      clearTimeout(timer);
      this.handshakeTimers.delete(connectionId);
    }
  }

  private socketOf(connectionId: string): HubSocket | undefined {
    return this.host.sockets().find((s) => s.getAttachment()?.connectionId === connectionId);
  }

  private authedAttachments(): Attachment[] {
    const out: Attachment[] = [];
    for (const socket of this.host.sockets()) {
      const a = socket.getAttachment();
      if (a?.stage === "authed") out.push(a);
    }
    return out;
  }

  private authedSocketOf(nodeId: string): { socket: HubSocket; attachment: Attachment } | undefined {
    let best: { socket: HubSocket; attachment: Attachment } | undefined;
    for (const socket of this.host.sockets()) {
      const a = socket.getAttachment();
      if (a?.stage !== "authed" || a.nodeId !== nodeId) continue;
      if (!best || best.attachment.connectedAt < a.connectedAt) best = { socket, attachment: a };
    }
    return best;
  }
}
