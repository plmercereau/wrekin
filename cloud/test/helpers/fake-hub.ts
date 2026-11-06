import type { HubClient, HubFactory, OnlineNode, Result } from "../../src/hub/client";
import type { CommandName } from "../../src/protocol";

export type RecordedCommand = { accountId: string; nodeId: string; name: CommandName; params: Record<string, unknown> };

/**
 * A hub that answers from scripted data: which nodes are online per account, and
 * what each command returns. Records every command for assertions.
 */
export class FakeHub {
  readonly online = new Map<string, OnlineNode[]>();
  readonly results = new Map<string, Result<unknown>>();
  readonly commands: RecordedCommand[] = [];
  /** Every `evict` the routes asked for, as `accountId/nodeId`. */
  readonly evicted: string[] = [];
  /** The accounts a live channel was opened for, in order. */
  readonly watched: string[] = [];
  /** The accounts told that their list changed, in order. */
  readonly changed: string[] = [];

  setOnline(accountId: string, nodes: OnlineNode[]) {
    this.online.set(accountId, nodes);
  }

  /** The result of the next `name` command for `nodeId`. */
  answer(nodeId: string, name: CommandName, result: Result<unknown>) {
    this.results.set(`${nodeId}/${name}`, result);
  }

  readonly factory: HubFactory = (accountId) => {
    const hub: HubClient = {
      online: async () => this.online.get(accountId) ?? [],
      command: async <T>(nodeId: string, name: CommandName, params: Record<string, unknown>) => {
        this.commands.push({ accountId, nodeId, name, params });
        const connected = (this.online.get(accountId) ?? []).some((n) => n.nodeId === nodeId);
        if (!connected) return { ok: false, error: { code: "node_offline", message: "the node has no open connection" } } as Result<T>;
        const scripted = this.results.get(`${nodeId}/${name}`);
        if (!scripted) return { ok: false, error: { code: "internal", message: "no scripted result" } } as Result<T>;
        return scripted as Result<T>;
      },
      connect: async () => new Response("no hub in this test", { status: 501 }),
      evict: async (nodeId: string) => {
        this.evicted.push(`${accountId}/${nodeId}`);
        this.online.set(accountId, (this.online.get(accountId) ?? []).filter((n) => n.nodeId !== nodeId));
      },
      // Plain Node cannot answer 101: the fake says which account it would have watched.
      watch: async () => {
        this.watched.push(accountId);
        return Response.json({ watching: accountId }, { status: 200 });
      },
      changed: async () => {
        this.changed.push(accountId);
      },
    };
    return hub;
  };
}
