import { env, SELF } from "cloudflare:test";
import { authSuite } from "./shared/auth-suite";

// The real thing: Better Auth on D1 inside workerd, with the migrations applied.
authSuite((path, init) => SELF.fetch(`${env.BASE_URL}${path}`, init), env.BASE_URL);
