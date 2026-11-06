import { env, SELF } from "cloudflare:test";
import { createAuth } from "../src/auth";
import { createTestSession } from "./helpers/session";
import { listNodesOverHttp } from "./helpers/trpc-http";
import { nodesSuite } from "./shared/nodes-suite";

// D1 and the real hub inside workerd. The hub reports nobody online: no node connects here.
const fetcher = (path: string, init?: RequestInit) => SELF.fetch(`${env.BASE_URL}${path}`, init);

nodesSuite(fetcher, (label) => createTestSession(createAuth(env), label), listNodesOverHttp(fetcher), env.BASE_URL);
