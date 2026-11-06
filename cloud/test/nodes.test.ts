import { env, SELF } from "cloudflare:test";
import { createAuth } from "../src/auth";
import { createTestSession } from "./helpers/session";
import { nodesSuite } from "./shared/nodes-suite";

// D1 and the real hub inside workerd. The hub reports nobody online: no node connects here.
nodesSuite(
  (path, init) => SELF.fetch(`${env.BASE_URL}${path}`, init),
  (label) => createTestSession(createAuth(env), label),
);
