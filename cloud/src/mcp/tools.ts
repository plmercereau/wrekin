import type { CallToolResult, McpServer } from "@modelcontextprotocol/server";
import { z } from "zod";
import type { NodeRecord, NodeStore } from "../db/nodes";
import type { HubClient, OnlineNode } from "../hub/client";
import { jobSchema, type CommandName, type ErrorCode, type Job } from "../protocol";
import { parseCloudSessionId } from "./session-id";

export type ToolContext = {
  /** The Better Auth user id behind the access token; every lookup is scoped to it. */
  accountId: string;
  nodes: NodeStore;
  /** The account's hub. */
  hub: HubClient;
};

/** Only harness of the MVP. */
export const HARNESS = "claude-code";

const text = (value: string): CallToolResult["content"] => [{ type: "text", text: value }];

export const ok = (structured: Record<string, unknown>, summary: string): CallToolResult => ({
  content: text(summary),
  structuredContent: structured,
});

/** Errors carry the protocol code in both the text and the structured content. */
export const fail = (code: ErrorCode, message: string): CallToolResult => ({
  content: text(`${code}: ${message}`),
  structuredContent: { error: { code, message } },
  isError: true,
});

const when = (iso: string | null | undefined) => (iso ? iso.replace("T", " ").replace(/\.\d+Z$/, "Z") : "never");

/** One line per node, for the text part of list_nodes. */
export function describeNodes(nodes: Array<{ name: string; online: boolean; lastSeenAt: string | null; harnesses?: OnlineNode["harnesses"] }>): string {
  if (nodes.length === 0) return "No machine is paired with this account. The user must run `wrekin auth` on a machine first.";
  const lines = nodes.map((n) => {
    const harness = n.harnesses?.find((h) => h.id === HARNESS);
    const detail = n.online
      ? harness
        ? harness.available
          ? `online, ${HARNESS} ${harness.version ?? "available"}`
          : `online, but ${HARNESS} is not installed there`
        : "online"
      : `offline, last seen ${when(n.lastSeenAt)}`;
    return `- ${n.name} (${detail})`;
  });
  return `${nodes.length} node${nodes.length === 1 ? "" : "s"}:\n${lines.join("\n")}`;
}

/** A paragraph that tells Claude what the job state means and what to do next. */
export function describeJob(job: Job, nodeName: string): string {
  const head = `Job ${job.id} on ${nodeName} for ${job.cloudSessionId} is ${job.state}.`;
  switch (job.state) {
    case "accepted":
    case "preparing":
    case "launching":
      return `${head} The node is working on it; call get_teleport_status with this job id to follow the progress.`;
    case "needs_attention":
      return `${head} ${job.attention?.message ?? "The node needs a decision."}${
        job.attention?.repositoryUrl ? ` Repository: ${job.attention.repositoryUrl}.` : ""
      }${job.attention?.proposedPath ? ` It would be cloned to ${job.attention.proposedPath}.` : ""} Ask the user whether to go ahead, then call approve_repository with approved true or false.`;
    case "ready": {
      const attach = job.session?.attach ? ` The user can open it with \`${job.session.attach}\`.` : "";
      if (job.ready && !job.ready.confirmed) {
        return `${head} The session is running but could not be verified (${job.ready.detail}).${attach} Tell the user to check it there.`;
      }
      return `${head} The session is live${job.workdir ? ` in ${job.workdir}` : ""}.${attach}`;
    }
    case "failed":
      return `${head} ${job.error ? `${job.error.code}: ${job.error.message}` : "No error detail was given."}${
        job.error?.code === "session_gone" ? " Call teleport again with relaunch true to start it anew." : ""
      }`;
  }
}

/** The common shape of a tool that sends one command to one node and expects a Job back. */
async function nodeJobCommand(
  ctx: ToolContext,
  nodeRef: string,
  name: CommandName,
  params: Record<string, unknown>,
): Promise<CallToolResult> {
  const node = await resolveNode(ctx, nodeRef);
  if (!node) return fail("unknown_node", `no node named or identified "${nodeRef}" on this account; call list_nodes`);
  const result = await ctx.hub.command(node.id, name, params);
  if (!result.ok) return fail(result.error.code, result.error.message);
  const job = jobSchema.safeParse(result.data);
  if (!job.success) return fail("internal", "the node answered with an unexpected job shape");
  return ok(job.data, describeJob(job.data, node.name));
}

/** A node by name or id, within the caller's account only (Review Focus 5). */
export async function resolveNode(ctx: ToolContext, ref: string): Promise<NodeRecord | null> {
  return ctx.nodes.findNode(ctx.accountId, ref.trim());
}

const nodeArg = z.string().min(1).describe("The target machine, by name or node id, as list_nodes returns it.");
const jobIdArg = z.string().min(1).describe("The job id returned by teleport.");

export function registerTools(server: McpServer, ctx: ToolContext): void {
  server.registerTool(
    "list_nodes",
    {
      title: "List the user's machines",
      description:
        "Lists the machines paired with the user's Wrekin account and whether each is connected right now, with the harnesses it can run. Call this before teleport when the destination is not obvious.",
    },
    async () => {
      const [rows, online] = await Promise.all([ctx.nodes.listNodes(ctx.accountId), ctx.hub.online()]);
      const nodes = rows.map((row) => {
        const live = online.find((n) => n.nodeId === row.id);
        return {
          id: row.id,
          name: row.name,
          online: live !== undefined,
          lastSeenAt: row.lastSeenAt,
          agentVersion: live?.agentVersion ?? row.agentVersion,
          harnesses: live?.harnesses ?? [],
        };
      });
      return ok({ nodes }, describeNodes(nodes));
    },
  );

  server.registerTool(
    "teleport",
    {
      title: "Teleport this Claude Code session to a machine",
      description:
        "Moves the current Claude Code Cloud session onto one of the user's machines, where it continues in a tmux session. " +
        "`session` must be this very Claude Code Cloud session: its id (session_…) or its URL (https://claude.ai/code/session_…), never a chat or conversation URL. " +
        "Returns a Job quickly; the work continues on the machine. Follow it with get_teleport_status. " +
        "A state of needs_attention means the machine needs the user's confirmation (for example to clone a new repository): ask the user, then call approve_repository. " +
        "A ready job with ready.confirmed false is running but unverified: tell the user to open it with the session.attach command. " +
        "Calling teleport again for the same session returns the existing job; pass relaunch true only when the previous session is gone (error session_gone).",
      inputSchema: z.object({
        node: nodeArg,
        session: z.string().min(1).describe("This Claude Code Cloud session: its id (session_…) or its https://claude.ai/code/session_… URL."),
        repository: z.string().min(1).optional().describe("The Git repository the session works on: a URL or owner/name on GitHub. Helps the machine find or clone the checkout."),
        directory: z.string().min(1).optional().describe("A directory on the machine to use, under one of its allowed roots. `~` is expanded there."),
        relaunch: z.boolean().optional().describe("Launch again when the previous session of this job has disappeared (after a session_gone error)."),
      }),
    },
    async ({ node, session, repository, directory, relaunch }) => {
      const cloudSessionId = parseCloudSessionId(session);
      if (!cloudSessionId) {
        return fail(
          "invalid_params",
          `"${session}" is not a Claude Code Cloud session. Pass the id of this Claude Code session (session_…) or its URL, https://claude.ai/code/session_…; a chat URL cannot be teleported.`,
        );
      }
      const params: Record<string, unknown> = { harness: HARNESS, cloudSessionId };
      if (repository !== undefined) params.repository = repository;
      if (directory !== undefined) params.directory = directory;
      if (relaunch !== undefined) params.relaunch = relaunch;
      return nodeJobCommand(ctx, node, "teleport", params);
    },
  );

  server.registerTool(
    "get_teleport_status",
    {
      title: "Read a teleport job",
      description:
        "Reads the current state of a teleport job on a machine: accepted, preparing, needs_attention, launching, ready or failed, with the tmux session to attach to once ready.",
      inputSchema: z.object({ node: nodeArg, jobId: jobIdArg }),
    },
    async ({ node, jobId }) => nodeJobCommand(ctx, node, "get_teleport_status", { jobId }),
  );

  server.registerTool(
    "approve_repository",
    {
      title: "Confirm or refuse the use of a new repository",
      description:
        "Answers a job in needs_attention: the machine asked whether it may clone and trust a repository it did not have. Call it only after the user decided. approved false fails the job with not_approved.",
      inputSchema: z.object({
        node: nodeArg,
        jobId: jobIdArg,
        approved: z.boolean().describe("The user's decision."),
      }),
    },
    async ({ node, jobId, approved }) => nodeJobCommand(ctx, node, "approve_repository", { jobId, approved }),
  );
}
