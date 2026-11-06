import type { Fetcher } from "../shared/auth-suite";

/** `nodes.list` over HTTP, as the browser calls it: a tRPC GET of a query without input, unwrapped. */
export const listNodesOverHttp =
  (fetcher: Fetcher) =>
  async (headers: Record<string, string>): Promise<Array<Record<string, unknown>>> => {
    const res = await fetcher("/api/trpc/nodes.list", { headers });
    if (!res.ok) throw new Error(`nodes.list answered ${res.status}`);
    const body = (await res.json()) as { result: { data: Array<Record<string, unknown>> } };
    return body.result.data;
  };
