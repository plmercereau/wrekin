import type { Env } from "../env";
import type { CommandName, ErrorCode, Harness, ProtocolError } from "../protocol";

/** A node with an authenticated socket, as the hub reports it. */
export type OnlineNode = {
  nodeId: string;
  connectedAt: string;
  agentVersion?: string;
  harnesses?: Harness[];
};

export type Result<T> = { ok: true; data: T } | { ok: false; error: ProtocolError };

/** What the routes and tools need from one account's Durable Object. */
export interface HubClient {
  online(): Promise<OnlineNode[]>;
  command<T = unknown>(nodeId: string, name: CommandName, params: Record<string, unknown>, timeoutMs?: number): Promise<Result<T>>;
}

export type HubFactory = (accountId: string) => HubClient;

/** The internal URL the Worker uses to address a hub; the host is never resolved. */
export const HUB_ORIGIN = "https://hub.internal";

/** A typed wrapper over the Durable Object stub of `accountId`. */
export function hubFor(env: Env, accountId: string): HubClient {
  const stub = env.ACCOUNT_HUB.get(env.ACCOUNT_HUB.idFromName(accountId));
  return {
    async online() {
      const res = await stub.fetch(`${HUB_ORIGIN}/nodes/online`);
      if (!res.ok) throw new Error(`hub /nodes/online answered ${res.status}`);
      const body = (await res.json()) as { nodes: OnlineNode[] };
      return body.nodes;
    },
    async command<T>(nodeId: string, name: CommandName, params: Record<string, unknown>, timeoutMs?: number) {
      const res = await stub.fetch(`${HUB_ORIGIN}/command`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ nodeId, name, params, timeoutMs }),
      });
      if (!res.ok) throw new Error(`hub /command answered ${res.status}`);
      return (await res.json()) as Result<T>;
    },
  };
}

export const failure = (code: ErrorCode, message: string): Result<never> => ({ ok: false, error: { code, message } });
