import { createServerFn } from "@tanstack/react-start";
import { getRequest } from "@tanstack/react-start/server";
import { z } from "zod";
import { createContext } from "../trpc/context";
import { createCaller } from "../trpc/router";
import { worker } from "./worker.server";

export type DeviceStatus = "pending" | "approved" | "denied" | "unknown";

/** The state of a device code (`wrekin auth` printed it), looked up with the visitor's session. */
export const getDeviceStatus = createServerFn({ method: "GET" })
  .validator(z.string().min(1))
  .handler(async ({ data: userCode }): Promise<DeviceStatus> => {
    const { services } = worker();
    try {
      const result = await services.auth.api.deviceVerify({ query: { user_code: userCode }, headers: getRequest().headers });
      return result.status === "pending" || result.status === "approved" || result.status === "denied" ? result.status : "unknown";
    } catch {
      // Better Auth answers an unknown or expired code with an error.
      return "unknown";
    }
  });

export type ConsentClient = { name: string; uri: string | null };

/** The OAuth client asking for access, by its id; falls back to the id when the lookup fails. */
export const getConsentClient = createServerFn({ method: "GET" })
  .validator(z.string().min(1))
  .handler(async ({ data: clientId }): Promise<ConsentClient> => {
    const { services } = worker();
    try {
      const client = await services.auth.api.getOAuthClientPublic({ query: { client_id: clientId }, headers: getRequest().headers });
      return { name: client.client_name ?? clientId, uri: client.client_uri ?? null };
    } catch {
      return { name: clientId, uri: null };
    }
  });

/** The signed-in user's nodes, through the same tRPC procedure the browser's Remove button uses. */
export const listMyNodes = createServerFn({ method: "GET" }).handler(async () => {
  const { env, services } = worker();
  const request = getRequest();
  return createCaller(await createContext({ request, env, services })).nodes.list();
});
