import { z } from 'zod';

export const environmentVariableScopeSchema = z.enum(['machine', 'user']);
export type EnvironmentVariableScope = z.infer<typeof environmentVariableScopeSchema>;

export const environmentVariableSchema = z.object({
  key: z.string().min(1),
  value: z.string(),
  scope: environmentVariableScopeSchema,
  length: z.number().int().nonnegative(),
  lastModifiedAt: z.string().optional(),
});
export type EnvironmentVariable = z.infer<typeof environmentVariableSchema>;

export const environmentSnapshotSchema = z.object({
  variables: z.array(environmentVariableSchema),
  count: z.number().int().nonnegative(),
  capturedAt: z.string(),
});
export type EnvironmentSnapshot = z.infer<typeof environmentSnapshotSchema>;

export const environmentMutationResultSchema = z.object({
  key: z.string().min(1),
  scope: environmentVariableScopeSchema,
  value: z.string().optional(),
  previousValue: z.string().nullable().optional(),
  operation: z.enum(['set', 'remove']),
  mutatedAt: z.string(),
  restartRequested: z.boolean().optional(),
});
export type EnvironmentMutationResult = z.infer<typeof environmentMutationResultSchema>;

const environmentCommandActionSchema = z.enum(['list', 'set', 'remove']);

export const environmentCommandRequestSchema = z.discriminatedUnion('action', [
	z.object({
		action: z.literal('list'),
		timestamp: z.string().datetime(),
		initiatedBy: z.string().optional()
	}),
	z.object({
		action: z.literal('set'),
		key: z.string().min(1),
		value: z.string(),
		scope: environmentVariableScopeSchema.default('user'),
		restartProcesses: z.boolean().optional(),
		timestamp: z.string().datetime(),
		initiatedBy: z.string().optional()
	}),
	z.object({
		action: z.literal('remove'),
		key: z.string().min(1),
		scope: environmentVariableScopeSchema.default('user'),
		timestamp: z.string().datetime(),
		initiatedBy: z.string().optional()
	})
]);
export type EnvironmentCommandRequest = z.infer<typeof environmentCommandRequestSchema>;

const environmentCommandSuccessSchema = z.discriminatedUnion('action', [
	z.object({
		action: z.literal('list'),
		status: z.literal('ok'),
		result: environmentSnapshotSchema
	}),
	z.object({
		action: z.literal('set'),
		status: z.literal('ok'),
		result: environmentMutationResultSchema
	}),
	z.object({
		action: z.literal('remove'),
		status: z.literal('ok'),
		result: environmentMutationResultSchema
	})
]);

const environmentCommandErrorSchema = z.object({
	action: environmentCommandActionSchema,
	status: z.literal('error'),
	error: z.string().min(1),
	code: z.string().optional()
});

export const environmentCommandResponseSchema = z.intersection(
	environmentCommandRequestSchema,
	z.union([environmentCommandSuccessSchema, environmentCommandErrorSchema])
);

export type EnvironmentCommandResponse = z.infer<typeof environmentCommandResponseSchema>;

export type EnvironmentCommandPayload = z.infer<typeof environmentCommandRequestSchema>;

