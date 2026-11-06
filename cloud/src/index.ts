import { Hono } from "hono";
import type { Env } from "./env";

export { AccountHub } from "./hub/account-hub";

const app = new Hono<{ Bindings: Env }>();

app.get("/", (c) => c.text("Wrekin"));

export default app;
