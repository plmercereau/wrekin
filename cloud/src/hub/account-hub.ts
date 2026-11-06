import { DurableObject } from "cloudflare:workers";
import type { Env } from "../env";

/** One instance per account; owns the node sockets and routes commands (filled in by Task B4). */
export class AccountHub extends DurableObject<Env> {
  async fetch(_request: Request): Promise<Response> {
    return new Response("not implemented", { status: 501 });
  }
}
