-- Nodes paired with an account. Only what the relay needs to authenticate and
-- route: never a private key, a token or any session output.
CREATE TABLE node (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES user(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  public_key TEXT NOT NULL UNIQUE,
  agent_version TEXT,
  platform TEXT,
  created_at TEXT NOT NULL,
  last_seen_at TEXT,
  UNIQUE(account_id, name)
);
CREATE INDEX node_account ON node(account_id);
