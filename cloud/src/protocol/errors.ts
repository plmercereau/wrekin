import { z } from "zod";

/** Stable error codes shared by the node, the relay and the MCP tools (PROTOCOL.md, "Error codes"). */
export const errorCodes = [
  "node_offline",
  "node_timeout",
  "unknown_node",
  "invalid_params",
  "unknown_job",
  "repository_required",
  "directory_not_allowed",
  "path_escapes_root",
  "harness_unavailable",
  "terminal_unavailable",
  "concurrency_limit",
  "attention_not_pending",
  "not_approved",
  "clone_failed",
  "git_prompt_blocked",
  "worktree_failed",
  "harness_exited",
  "session_gone",
  "internal",
] as const;

export type ErrorCode = (typeof errorCodes)[number];

export const errorCodeSchema = z.enum(errorCodes);

export const protocolErrorSchema = z.object({
  code: errorCodeSchema,
  message: z.string(),
});

export type ProtocolError = z.infer<typeof protocolErrorSchema>;

export const protocolError = (code: ErrorCode, message: string): ProtocolError => ({ code, message });
