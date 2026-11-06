import { Kysely } from "kysely";
import { D1Dialect } from "kysely-d1";
import type { Env } from "../env";
import type { Database } from "./schema";

const cache = new WeakMap<D1Database, Kysely<Database>>();

/** A Kysely instance over the D1 binding, one per database. */
export function getDb(env: Env): Kysely<Database> {
  const cached = cache.get(env.DB);
  if (cached) return cached;
  const db = new Kysely<Database>({ dialect: new D1Dialect({ database: env.DB }) });
  cache.set(env.DB, db);
  return db;
}
