import { z } from "zod";
import { protocolErrorSchema } from "./errors";

/** The six job states of PROTOCOL.md, "Job". */
export const jobStates = ["accepted", "preparing", "needs_attention", "launching", "ready", "failed"] as const;
export type JobState = (typeof jobStates)[number];
export const jobStateSchema = z.enum(jobStates);

/** Sub-objects are `null` when not applicable; a missing one is read as `null`. */
const nullable = <T extends z.ZodType>(schema: T) => schema.nullish().transform((value) => value ?? null);

export const repositorySchema = z.object({
  input: z.string(),
  url: z.string(),
  name: z.string(),
});

export const sessionSchema = z.object({
  terminal: z.string(),
  name: z.string(),
  attach: z.string(),
});

export const attentionSchema = z.object({
  kind: z.string(),
  message: z.string(),
  repositoryUrl: z.string().optional(),
  proposedPath: z.string().optional(),
});

export const readySchema = z.object({
  confirmed: z.boolean(),
  detail: z.string(),
});

export const jobSchema = z.object({
  id: z.string(),
  harness: z.string(),
  cloudSessionId: z.string(),
  state: jobStateSchema,
  repository: nullable(repositorySchema),
  workdir: nullable(z.string()),
  session: nullable(sessionSchema),
  attention: nullable(attentionSchema),
  ready: nullable(readySchema),
  error: nullable(protocolErrorSchema),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type Job = z.infer<typeof jobSchema>;

export const jobListSchema = z.object({ jobs: z.array(jobSchema) });

// Command parameters, validated on the relay before a command leaves for the node.

export const teleportParamsSchema = z.object({
  harness: z.string().min(1),
  cloudSessionId: z.string().min(1),
  repository: z.string().min(1).optional(),
  directory: z.string().min(1).optional(),
  relaunch: z.boolean().optional(),
});
export type TeleportParams = z.infer<typeof teleportParamsSchema>;

export const statusParamsSchema = z.object({ jobId: z.string().min(1) });
export type StatusParams = z.infer<typeof statusParamsSchema>;

export const approveParamsSchema = z.object({ jobId: z.string().min(1), approved: z.boolean() });
export type ApproveParams = z.infer<typeof approveParamsSchema>;
