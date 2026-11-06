import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { createTrpcClient, TRPCProvider } from "./client/trpc";
import { routeTree } from "./routeTree.gen";

/** Called by TanStack Start once per request on the server and once in the browser. */
export function getRouter() {
  const queryClient = new QueryClient();
  const trpcClient = createTrpcClient();
  return createRouter({
    routeTree,
    scrollRestoration: true,
    Wrap: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>
        <TRPCProvider trpcClient={trpcClient} queryClient={queryClient}>
          {children}
        </TRPCProvider>
      </QueryClientProvider>
    ),
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>;
  }
}
