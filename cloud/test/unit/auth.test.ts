import { nodeWorker, TEST_BASE_URL } from "../helpers/node-app";
import { authSuite } from "../shared/auth-suite";

const { fetcher } = nodeWorker();
authSuite(fetcher, TEST_BASE_URL);
