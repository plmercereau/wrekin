import { env, SELF } from "cloudflare:test";
import { createAuth } from "../src/auth";
import { createTestSession } from "./helpers/session";
import { pagesSuite } from "./shared/pages-suite";

pagesSuite(
  (path, init) => SELF.fetch(`${env.BASE_URL}${path}`, init),
  env.BASE_URL,
  (label) => createTestSession(createAuth(env), label),
);
