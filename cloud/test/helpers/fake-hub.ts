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
    };
    return hub;
  };
}
