import { authSuite } from "../shared/auth-suite";
import { nodeApp, TEST_BASE_URL } from "../helpers/node-app";

const { fetcher } = nodeApp();
authSuite(fetcher, TEST_BASE_URL);
