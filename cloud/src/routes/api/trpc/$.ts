import { createFileRoute } from "@tanstack/react-router";
import { fetchRequestHandler } from "@trpc/server/adapters/fetch";
import { worker } from "../../../server/worker.server";
import { createContext } from "../../../trpc/context";
import { appRouter } from "../../../trpc/router";

/** The browser's API: every procedure of src/trpc/router.ts, over tRPC's fetch adapter. */
const handle = ({ request }: { request: Request }) => {
  const { env, services } = worker();
  return fetchRequestHandler({
    endpoint: "/api/trpc",
    req: request,
    router: appRouter,
    createContext: () => createContext({ request, env, services }),
    onError: ({ error, path }) => {
      if (error.code === "INTERNAL_SERVER_ERROR") console.error(`trpc ${path ?? "?"}`, error);
    },
  });
};

export const Route = createFileRoute("/api/trpc/$")({
  server: { handlers: { GET: handle, POST: handle } },
});
