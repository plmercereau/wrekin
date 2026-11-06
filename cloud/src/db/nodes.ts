import type { Kysely } from "kysely";
import { randomId } from "../crypto";
import type { Database, NodeTable } from "./schema";

/** A paired node as the rest of the Worker sees it (camelCase, no SQL column names). */
export type NodeRecord = {
  id: string;
  accountId: string;
  name: string;
  publicKey: string;
  agentVersion: string | null;
  platform: string | null;
  createdAt: string;
  lastSeenAt: string | null;
};

export type NewNode = {
  accountId: string;
  name: string;
  publicKey: string;
  agentVersion?: string | null;
  platform?: string | null;
};

/** Raised by `insertNode` when `(account, name)` or the public key is already registered. */
export class NodeConflictError extends Error {
  constructor(readonly field: "name" | "publicKey") {
    super(field === "name" ? "a node of that name exists on this account" : "that public key is already registered");
    this.name = "NodeConflictError";
  }
}

/** What the routes and tools need from the node table; D1 in the Worker, memory in the Node tests. */
export interface NodeStore {
  insertNode(input: NewNode): Promise<NodeRecord>;
  listNodes(accountId: string): Promise<NodeRecord[]>;
  /** By id or by name, always within one account (a node of another account is "unknown"). */
  findNode(accountId: string, idOrName: string): Promise<NodeRecord | null>;
  findNodeById(id: string): Promise<NodeRecord | null>;
  deleteNode(accountId: string, id: string): Promise<boolean>;
  touchNode(id: string, lastSeenAt: string): Promise<void>;
}

export const newNodeId = (): string => randomId("node");

const toRecord = (row: NodeTable): NodeRecord => ({
  id: row.id,
  accountId: row.account_id,
  name: row.name,
  publicKey: row.public_key,
  agentVersion: row.agent_version,
  platform: row.platform,
  createdAt: row.created_at,
  lastSeenAt: row.last_seen_at,
});

export class KyselyNodeStore implements NodeStore {
  constructor(private readonly db: Kysely<Database>) {}

  async insertNode(input: NewNode): Promise<NodeRecord> {
    const sameName = await this.db
      .selectFrom("node")
      .select("id")
      .where("account_id", "=", input.accountId)
      .where("name", "=", input.name)
      .executeTakeFirst();
    if (sameName) throw new NodeConflictError("name");
    const sameKey = await this.db
      .selectFrom("node")
      .select("id")
      .where("public_key", "=", input.publicKey)
      .executeTakeFirst();
    if (sameKey) throw new NodeConflictError("publicKey");
    const row: NodeTable = {
      id: newNodeId(),
      account_id: input.accountId,
      name: input.name,
      public_key: input.publicKey,
      agent_version: input.agentVersion ?? null,
      platform: input.platform ?? null,
      created_at: new Date().toISOString(),
      last_seen_at: null,
    };
    try {
      await this.db.insertInto("node").values(row).execute();
    } catch (error) {
      // Two registrations racing past the checks above: the UNIQUE constraints decide.
      const message = error instanceof Error ? error.message : String(error);
      if (message.includes("UNIQUE")) throw new NodeConflictError(message.includes("public_key") ? "publicKey" : "name");
      throw error;
    }
    return toRecord(row);
  }

  async listNodes(accountId: string): Promise<NodeRecord[]> {
    const rows = await this.db
      .selectFrom("node")
      .selectAll()
      .where("account_id", "=", accountId)
      .orderBy("created_at", "asc")
      .execute();
    return rows.map(toRecord);
  }

  async findNode(accountId: string, idOrName: string): Promise<NodeRecord | null> {
    const row = await this.db
      .selectFrom("node")
      .selectAll()
      .where("account_id", "=", accountId)
      .where((eb) => eb.or([eb("id", "=", idOrName), eb("name", "=", idOrName)]))
      .executeTakeFirst();
    return row ? toRecord(row) : null;
  }

  async findNodeById(id: string): Promise<NodeRecord | null> {
    const row = await this.db.selectFrom("node").selectAll().where("id", "=", id).executeTakeFirst();
    return row ? toRecord(row) : null;
  }

  async deleteNode(accountId: string, id: string): Promise<boolean> {
    const result = await this.db
      .deleteFrom("node")
      .where("account_id", "=", accountId)
      .where("id", "=", id)
      .executeTakeFirst();
    return Number(result.numDeletedRows) > 0;
  }

  async touchNode(id: string, lastSeenAt: string): Promise<void> {
    await this.db.updateTable("node").set({ last_seen_at: lastSeenAt }).where("id", "=", id).execute();
  }
}
