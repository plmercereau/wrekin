/** The `node` table of migrations/0002_nodes.sql. Better Auth owns the other tables. */
export interface NodeTable {
  id: string;
  account_id: string;
  name: string;
  public_key: string;
  agent_version: string | null;
  platform: string | null;
  created_at: string;
  last_seen_at: string | null;
}

export interface Database {
  node: NodeTable;
}
