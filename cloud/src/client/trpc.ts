import { createTRPCClient, httpBatchLink } from "@trpc/client";
import { createTRPCContext } from "@trpc/tanstack-react-query";
import type { AppRouter } from "../trpc/router";

export const { TRPCProvider, useTRPC } = createTRPCContext<AppRouter>();

/** One client per router instance; same origin, cookies travel by themselves. */
export function createTrpcClient() {
  return createTRPCClient<AppRouter>({ links: [httpBatchLink({ url: "/api/trpc" })] });
}
