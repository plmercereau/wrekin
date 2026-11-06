import type { BetterAuthOptions } from "better-auth";
import { describe, expect, it } from "vitest";
import { nodeWorker, TEST_BASE_URL } from "../helpers/node-app";
import { authSuite } from "../shared/auth-suite";

const { fetcher, services } = nodeWorker();
authSuite(fetcher, TEST_BASE_URL);

describe("account linking", () => {
  it("trusts no provider, so a second provider is linked only on an e-mail it reports as verified", () => {
    // Widened: the instance's own type narrows the option to the literal it was built with.
    const linking = (services.auth.options as BetterAuthOptions).account?.accountLinking;
    expect(linking?.enabled).toBe(true);
    // Better Auth skips the `emailVerified` requirement for a provider named here.
    expect(linking?.trustedProviders ?? []).toEqual([]);
  });
});
