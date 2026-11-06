import type { Env } from "../env";
import type { Services } from "../services";

/** The signed-in user as the procedures and the pages see it. */
export type SessionUser = { id: string; name: string; email: string };

export type Context = {
  env: Env;
  services: Services;
  /** null without a session cookie or `Authorization: Bearer <session token>`. */
  user: SessionUser | null;
};

/** One context per request: the session is read once, from the cookie or the bearer header. */
export async function createContext(opts: { request: Request; env: Env; services: Services }): Promise<Context> {
  const session = await opts.services.auth.api.getSession({ headers: opts.request.headers });
  return {
    env: opts.env,
    services: opts.services,
    user: session ? { id: session.user.id, name: session.user.name, email: session.user.email } : null,
  };
}
