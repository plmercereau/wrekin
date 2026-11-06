import { createServerFn } from "@tanstack/react-start";
import { getRequest } from "@tanstack/react-start/server";
import type { SessionUser } from "../trpc/context";
import { worker } from "./worker.server";

/** What every page needs: the public origin and the signed-in user, read once per navigation by the root route. */
export type PageContext = { baseURL: string; user: SessionUser | null };

export const getPageContext = createServerFn({ method: "GET" }).handler(async (): Promise<PageContext> => {
  const { env, services } = worker();
  const session = await services.auth.api.getSession({ headers: getRequest().headers });
  return {
    baseURL: env.BASE_URL,
    user: session ? { id: session.user.id, name: session.user.name, email: session.user.email } : null,
  };
});
