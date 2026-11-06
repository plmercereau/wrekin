import { TRPCError } from "@trpc/server";
import { z } from "zod";
import {
  listNodes,
  NodeServiceError,
  registerNode,
  registerSchema,
  removeNode,
  unregisterNode,
  unregisterSchema,
} from "../nodes/service";
import { createCallerFactory, protectedProcedure, publicProcedure, router } from "./init";

const codeFor = { 400: "BAD_REQUEST", 401: "UNAUTHORIZED", 404: "NOT_FOUND", 409: "CONFLICT" } as const;

/** A refusal of the node service becomes a tRPC error whose message starts with the REST code (`name_taken: …`). */
function rethrow(error: unknown): never {
  if (error instanceof NodeServiceError) {
    throw new TRPCError({ code: codeFor[error.status], message: `${error.code}: ${error.message}`, cause: error });
  }
  throw error;
}

/**
 * The browser's API. `nodes.register` and `nodes.unregister` are the same operations the Go
 * agent reaches through the plain `POST /api/nodes` and `POST /api/nodes/unregister` routes
 * (src/nodes/service.ts holds the one implementation).
 */
export const appRouter = router({
  session: router({
    /** The signed-in user, or null. */
    me: publicProcedure.query(({ ctx }) => ctx.user),
  }),
  nodes: router({
    list: protectedProcedure.query(({ ctx }) => listNodes(ctx.services, ctx.user.id)),
    remove: protectedProcedure.input(z.object({ id: z.string().min(1) })).mutation(async ({ ctx, input }) => {
      const removed = await removeNode(ctx.services, ctx.user.id, input.id);
      if (!removed) throw new TRPCError({ code: "NOT_FOUND", message: "unknown_node: no node with that id on this account" });
      return { id: input.id };
    }),
    register: protectedProcedure
      .input(registerSchema)
      .mutation(({ ctx, input }) => registerNode(ctx.services, ctx.user.id, input).catch(rethrow)),
    // Signed with the node's key, no session involved: public on purpose.
    unregister: publicProcedure
      .input(unregisterSchema)
      .mutation(({ ctx, input }) => unregisterNode(ctx.services, input).then(() => ({ nodeId: input.nodeId })).catch(rethrow)),
  }),
});

export type AppRouter = typeof appRouter;

/** Calls procedures in-process with a ready context (the tests, and server code that already holds one). */
export const createCaller = createCallerFactory(appRouter);
