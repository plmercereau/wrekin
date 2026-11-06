import { newNodeId, NodeConflictError, type NewNode, type NodeRecord, type NodeStore } from "../../src/db/nodes";

/** The node table in an array, for the plain-Node tests. */
export class MemoryNodeStore implements NodeStore {
  readonly rows: NodeRecord[] = [];

  async insertNode(input: NewNode): Promise<NodeRecord> {
    if (this.rows.some((r) => r.accountId === input.accountId && r.name === input.name)) throw new NodeConflictError("name");
    if (this.rows.some((r) => r.publicKey === input.publicKey)) throw new NodeConflictError("publicKey");
    const row: NodeRecord = {
      id: newNodeId(),
      accountId: input.accountId,
      name: input.name,
      publicKey: input.publicKey,
      agentVersion: input.agentVersion ?? null,
      platform: input.platform ?? null,
      createdAt: new Date().toISOString(),
      lastSeenAt: null,
    };
    this.rows.push(row);
    return { ...row };
  }

  async listNodes(accountId: string): Promise<NodeRecord[]> {
    return this.rows.filter((r) => r.accountId === accountId).map((r) => ({ ...r }));
  }

  async findNode(accountId: string, idOrName: string): Promise<NodeRecord | null> {
    const row = this.rows.find((r) => r.accountId === accountId && (r.id === idOrName || r.name === idOrName));
    return row ? { ...row } : null;
  }

  async findNodeById(id: string): Promise<NodeRecord | null> {
    const row = this.rows.find((r) => r.id === id);
    return row ? { ...row } : null;
  }

  async deleteNode(accountId: string, id: string): Promise<boolean> {
    const index = this.rows.findIndex((r) => r.accountId === accountId && r.id === id);
    if (index < 0) return false;
    this.rows.splice(index, 1);
    return true;
  }

  async touchNode(id: string, lastSeenAt: string): Promise<void> {
    const row = this.rows.find((r) => r.id === id);
    if (row) row.lastSeenAt = lastSeenAt;
  }
}
