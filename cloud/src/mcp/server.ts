import { McpServer } from "@modelcontextprotocol/server";
import { registerTools, type ToolContext } from "./tools";

export const SERVER_NAME = "wrekin";
export const SERVER_VERSION = "0.1.0";

const instructions = `Wrekin teleports the current Claude Code Cloud session onto one of the user's own machines.
Typical flow: list_nodes to pick a connected machine, teleport with this session's id or https://claude.ai/code/session_… URL, then get_teleport_status until the job is ready or failed.
When a job is needs_attention, ask the user and call approve_repository. When a job is ready, give the user the session.attach command. Nothing here runs arbitrary commands or shows terminal output.`;

/** A fresh server for one request of one account: nothing is kept between requests. */
export function buildMcpServer(ctx: ToolContext): McpServer {
  const server = new McpServer({ name: SERVER_NAME, version: SERVER_VERSION }, { instructions });
  registerTools(server, ctx);
  return server;
}
