import { initTRPC, TRPCError } from "@trpc/server";
import type { Context } from "./context";

const t = initTRPC.context<Context>().create();

export const router = t.router;
export const createCallerFactory = t.createCallerFactory;
export const publicProcedure = t.procedure;

/** Refuses with UNAUTHORIZED unless the request carries a session; the user is then non-null in `ctx`. */
export const protectedProcedure = t.procedure.use(({ ctx, next }) => {
  if (!ctx.user) throw new TRPCError({ code: "UNAUTHORIZED", message: "unauthenticated: sign in first" });
  return next({ ctx: { ...ctx, user: ctx.user } });
});
