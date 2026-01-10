import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { gzipSync } from 'node:zlib';
import { db } from '$lib/server/db/index.js';
import { plugin as pluginTable } from '$lib/server/db/schema.js';
import { eq } from 'drizzle-orm';
import { refreshSignaturePolicy } from '$lib/server/plugins/signature-policy.js';
import { PluginTelemetryStore } from '$lib/server/plugins/telemetry-store.js';
import JSZip from 'jszip';
import { pack as createTarPack } from 'tar-stream';

const RELEASE_SIGNER = 'release';
const RELEASE_PUBLIC_KEY = 'ea9ceca1c7c7176859b235e095cbca9b5755746b741865cab5458d6f0e754cc2';

const toBuffer = (value: string | Uint8Array | Buffer): Buffer =>
	typeof value === 'string' ? Buffer.from(value, 'utf8') : Buffer.from(value);

const toArrayBuffer = (buffer: Buffer): ArrayBuffer => {
	const view = new Uint8Array(buffer.byteLength);
	view.set(buffer);
	return view.buffer;
};

const createZipPluginPackage = async (files: Record<string, string | Uint8Array | Buffer>) => {
	const zip = new JSZip();
	for (const [name, content] of Object.entries(files)) {
		zip.file(name, toBuffer(content));
	}
	return zip.generateAsync({ type: 'nodebuffer' });
};

const createTarGzPluginPackage = async (files: Record<string, string | Uint8Array | Buffer>) => {
	const pack = createTarPack();
	const chunks: Buffer[] = [];
	pack.on('data', (chunk: Buffer) => {
		chunks.push(Buffer.from(chunk));
	});
	const completion = new Promise<void>((resolve, reject) => {
		pack.on('end', () => resolve());
		pack.on('error', (err: Error) => reject(err));
	});
	for (const [name, content] of Object.entries(files)) {
		await new Promise<void>((resolveEntry, rejectEntry) => {
			pack.entry({ name }, toBuffer(content), (err?: Error | null) => {
				if (err) {
					rejectEntry(err);
				} else {
					resolveEntry();
				}
			});
		});
	}
	pack.finalize();
	await completion;
	const tarBuffer = Buffer.concat(chunks);
	return gzipSync(tarBuffer);
};

const mockEnv = vi.hoisted(() => {
	process.env.DATABASE_URL = ':memory:';
	const env: Record<string, string | undefined> = { DATABASE_URL: ':memory:' };
	return { env };
});

vi.mock('$env/dynamic/private', () => mockEnv);

const authorizeAgent = vi.fn();
const getAgent = vi.fn();

vi.mock('$lib/server/rat/store.js', async () => {
	const mod = await vi.importActual<typeof import('$lib/server/rat/store.js')>(
		'$lib/server/rat/store.js'
	);
	return {
		...mod,
		registry: {
			...mod.registry,
			authorizeAgent,
			getAgent
		},
		RegistryError: mod.RegistryError
	};
});

describe('agent plugin API', () => {
	let manifestDir: string;
	let trustDir: string;
	let trustPath: string;
	const manifestId = 'test-plugin';
	const artifactContent = 'artifact payload';
	const developerUser = {
		id: 'developer-1',
		role: 'developer',
		passkeyRegistered: true,
		voucherId: 'voucher-1',
		voucherActive: true,
		voucherExpiresAt: null
	} as const;

	beforeEach(async () => {
		manifestDir = mkdtempSync(join(tmpdir(), 'tenvy-agent-manifests-'));
		const manifestPath = join(manifestDir, `${manifestId}.json`);
		const artifactPath = join(manifestDir, 'pkg.zip');
		const artifactBuffer = Buffer.from(artifactContent, 'utf8');
		const artifactHash = createHash('sha256').update(artifactBuffer).digest('hex');
		writeFileSync(artifactPath, artifactBuffer);
		writeFileSync(
			manifestPath,
			JSON.stringify({
				id: manifestId,
				name: 'Test Plugin',
				version: '1.0.0',
				entry: 'plugin.exe',
				repositoryUrl: 'https://github.com/rootbay/test-plugin',
				license: { spdxId: 'MIT' },
				requirements: {},
				distribution: {
					defaultMode: 'automatic',
					autoUpdate: true,
					signature: 'sha256',
					signatureHash: artifactHash
				},
				package: {
					artifact: 'pkg.zip',
					hash: artifactHash,
					sizeBytes: artifactBuffer.byteLength
				}
			})
		);

		trustDir = mkdtempSync(join(tmpdir(), 'tenvy-plugin-trust-'));
		trustPath = join(trustDir, 'trust.json');
		writeFileSync(
			trustPath,
			JSON.stringify({
				sha256AllowList: [artifactHash],
				ed25519PublicKeys: { [RELEASE_SIGNER]: RELEASE_PUBLIC_KEY }
			})
		);

		process.env.TENVY_PLUGIN_MANIFEST_DIR = manifestDir;
		process.env.TENVY_PLUGIN_TRUST_CONFIG = trustPath;
		Object.assign(mockEnv.env, {
			DATABASE_URL: ':memory:',
			TENVY_PLUGIN_MANIFEST_DIR: manifestDir,
			TENVY_PLUGIN_TRUST_CONFIG: trustPath
		});

		refreshSignaturePolicy();

		const sharedModule = await import('../src/routes/api/agents/[id]/plugins/_shared.js');
		sharedModule.telemetryStore.reset();

		// Insert required records for foreign keys
		const { voucher, user, agent } = await import('../src/lib/server/db/schema.js');
		db.insert(voucher)
			.values({ id: 'voucher-1', codeHash: 'hash', createdAt: new Date() })
			.onConflictDoNothing()
			.run();
		db.insert(user)
			.values({ ...developerUser, createdAt: new Date() })
			.onConflictDoNothing()
			.run();
		db.insert(agent)
			.values({
				id: 'agent-1',
				keyHash: 'hash',
				metadata: JSON.stringify({ hostname: 'test' }),
				status: 'online',
				connectedAt: new Date(),
				lastSeen: new Date(),
				config: JSON.stringify({}),
				fingerprint: 'fingerprint-1',
				createdAt: new Date(),
				updatedAt: new Date()
			})
			.onConflictDoNothing()
			.run();

		authorizeAgent.mockReset();
		getAgent.mockReset();
		getAgent.mockReturnValue({ id: 'agent-1' });
	});

	afterEach(async () => {
		await db.delete(pluginTable);
		rmSync(manifestDir, { recursive: true, force: true });
		rmSync(trustDir, { recursive: true, force: true });
		delete process.env.TENVY_PLUGIN_MANIFEST_DIR;
		delete process.env.TENVY_PLUGIN_TRUST_CONFIG;
		Object.keys(mockEnv.env).forEach((key) => {
			if (key !== 'DATABASE_URL') {
				delete mockEnv.env[key];
			}
		});
		mockEnv.env.DATABASE_URL = ':memory:';
	});

	it('returns manifest snapshots and artifacts for authorized agents', async () => {
		const sharedModule = await import('../src/routes/api/agents/[id]/plugins/_shared.js');
		const { telemetryStore } = sharedModule;

		// 1. Initial load to populate the 'plugin' table from manifests
		await telemetryStore.getManifestSnapshot();

		// 2. Approve the plugin in the database
		await db
			.update(pluginTable)
			.set({ approvalStatus: 'approved', approvedAt: new Date() })
			.where(eq(pluginTable.id, manifestId));

		// 3. Invalidate snapshot to force rebuild with approved status
		telemetryStore.invalidateManifestSnapshot();
		const snapshotResult = await telemetryStore.getManifestSnapshot();
		expect(snapshotResult.manifests.some((m) => m.pluginId === manifestId)).toBe(true);

		const listModule = await import('../src/routes/api/clients/[id]/plugins/+server.js');
		const manifestModule = await import(
			'../src/routes/api/agents/[id]/plugins/[pluginId]/+server.js'
		);
		const artifactModule = await import(
			'../src/routes/api/agents/[id]/plugins/[pluginId]/artifact/+server.js'
		);

		const uiResponse = await listModule.GET({
			params: { id: 'agent-1' },
			request: new Request('https://controller.test/api/clients/agent-1/plugins', {
				headers: { Accept: 'application/json' }
			}),
			url: new URL('https://controller.test/api/clients/agent-1/plugins')
		} as Parameters<typeof listModule.GET>[0]);

		expect(getAgent).toHaveBeenCalledWith('agent-1');

		const uiPayload = (await uiResponse.json()) as { plugins: Array<{ id: string }> };
		expect(uiPayload.plugins[0]?.id).toBe(manifestId);

		const requestHeaders = {
			Authorization: 'Bearer agent-key',
			Accept: 'application/vnd.tenvy.plugin-manifest+json'
		};

		const listResponse = await listModule.GET({
			params: { id: 'agent-1' },
			request: new Request('https://controller.test/api/clients/agent-1/plugins', {
				headers: requestHeaders
			}),
			url: new URL('https://controller.test/api/clients/agent-1/plugins')
		} as Parameters<typeof listModule.GET>[0]);

		expect(authorizeAgent).toHaveBeenCalledWith('agent-1', 'agent-key');
		expect(listResponse.headers.get('content-type')).toContain(
			'application/vnd.tenvy.plugin-manifest+json'
		);

		const snapshot = (await listResponse.json()) as {
			version: string;
			manifests: Array<{ pluginId: string; manifestDigest: string }>;
		};
		expect(snapshot.manifests[0]?.pluginId).toBe(manifestId);

		const manifestResponse = await manifestModule.GET({
			params: { id: 'agent-1', pluginId: manifestId },
			request: new Request('https://controller.test', { headers: requestHeaders }),
			url: new URL(`https://controller.test/api/agents/agent-1/plugins/${manifestId}`)
		} as Parameters<typeof manifestModule.GET>[0]);

		const manifestText = await manifestResponse.text();
		expect(manifestText).toContain(`"id":"${manifestId}"`);

		const artifactResponse = await artifactModule.GET({
			params: { id: 'agent-1', pluginId: manifestId },
			request: new Request('https://controller.test', { headers: requestHeaders }),
			url: new URL(`https://controller.test/api/agents/agent-1/plugins/${manifestId}/artifact`)
		} as Parameters<typeof artifactModule.GET>[0]);

		const artifactBuffer = await artifactResponse.arrayBuffer();
		expect(Buffer.from(artifactBuffer).toString()).toBe(artifactContent);
	});

	it('accepts plugin uploads from zip archives and persists runtime metadata', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const artifactPayload = 'uploaded artifact payload';
		const artifactBuffer = Buffer.from(artifactPayload, 'utf8');
		const artifactHash = createHash('sha256').update(artifactBuffer).digest('hex');
		const existingHash = createHash('sha256').update(artifactContent, 'utf8').digest('hex');

		writeFileSync(
			trustPath,
			JSON.stringify({
				sha256AllowList: [existingHash, artifactHash],
				ed25519PublicKeys: { [RELEASE_SIGNER]: RELEASE_PUBLIC_KEY }
			})
		);
		refreshSignaturePolicy();

		const manifest = {
			id: 'uploaded-plugin',
			name: 'Uploaded Plugin',
			version: '1.0.0',
			entry: 'uploaded.exe',
			repositoryUrl: 'https://github.com/rootbay/uploaded-plugin',
			license: { spdxId: 'MIT' },
			requirements: { requiredModules: [] },
			distribution: {
				defaultMode: 'manual',
				autoUpdate: false,
				signature: 'sha256',
				signatureHash: artifactHash
			},
			package: {
				artifact: 'uploaded.bin',
				hash: artifactHash,
				sizeBytes: artifactBuffer.byteLength
			}
		} satisfies Record<string, unknown>;

		const archiveBuffer = await createZipPluginPackage({
			'manifest.json': JSON.stringify(manifest),
			'uploaded.bin': artifactBuffer
		});

		const form = new FormData();
		form.set(
			'artifact',
			new File([toArrayBuffer(archiveBuffer)], 'uploaded.zip', {
				type: 'application/octet-stream'
			})
		);

		const response = await POST({
			locals: { user: developerUser },
			request: new Request('https://controller.test/api/plugins', {
				method: 'POST',
				body: form
			})
		} as Parameters<typeof POST>[0]);

		expect(response.status).toBe(201);
		const body = (await response.json()) as {
			plugin: { id: string; version: string; approvalStatus?: string };
			approvalStatus: string;
		};
		expect(body.plugin.id).toBe('uploaded-plugin');
		expect(body.plugin.version).toBe('1.0.0');
		expect(body.approvalStatus).toBe('pending');

		const [row] = await db.select().from(pluginTable).where(eq(pluginTable.id, 'uploaded-plugin'));
		expect(row?.approvalStatus).toBe('pending');
	});

	it('accepts plugin uploads from tar.gz archives', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const artifactBuffer = Buffer.from('tar payload', 'utf8');
		const artifactHash = createHash('sha256').update(artifactBuffer).digest('hex');
		const existingHash = createHash('sha256').update(artifactContent, 'utf8').digest('hex');

		writeFileSync(
			trustPath,
			JSON.stringify({
				sha256AllowList: [existingHash, artifactHash],
				ed25519PublicKeys: { [RELEASE_SIGNER]: RELEASE_PUBLIC_KEY }
			})
		);
		refreshSignaturePolicy();

		const manifest = {
			id: 'tar-plugin',
			name: 'Tar Plugin',
			version: '1.0.0',
			entry: 'tar.exe',
			requirements: {},
			distribution: {
				defaultMode: 'manual',
				autoUpdate: false,
				signature: 'sha256',
				signatureHash: artifactHash
			},
			package: {
				artifact: 'tar.bin',
				hash: artifactHash
			}
		} satisfies Record<string, unknown>;

		const archiveBuffer = await createTarGzPluginPackage({
			'manifest.json': JSON.stringify(manifest),
			'tar.bin': artifactBuffer
		});

		const form = new FormData();
		form.set('artifact', new File([toArrayBuffer(archiveBuffer)], 'tar-plugin.tar.gz'));

		const response = await POST({
			locals: { user: developerUser },
			request: new Request('https://controller.test/api/plugins', {
				method: 'POST',
				body: form
			})
		} as Parameters<typeof POST>[0]);

		expect(response.status).toBe(201);
	});

	it('rejects uploads with invalid manifests', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const archiveBuffer = await createZipPluginPackage({
			'manifest.json': JSON.stringify({
				id: '',
				name: '',
				version: 'not-a-version',
				entry: '',
				requirements: {},
				distribution: { defaultMode: 'invalid', autoUpdate: false, signature: 'sha256' },
				package: { artifact: '', hash: '' }
			}),
			'artifact.bin': 'broken'
		});

		const form = new FormData();
		form.set('artifact', new File([toArrayBuffer(archiveBuffer)], 'invalid.zip'));

		await expect(
			POST({
				locals: { user: developerUser },
				request: new Request('https://controller.test/api/plugins', {
					method: 'POST',
					body: form
				})
			} as Parameters<typeof POST>[0])
		).rejects.toMatchObject({ status: 400 });
	});

	it('rejects uploads when manifest is missing from archive', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const archiveBuffer = await createZipPluginPackage({ 'plugin.bin': 'content' });

		const form = new FormData();
		form.set('artifact', new File([toArrayBuffer(archiveBuffer)], 'no-manifest.zip'));

		await expect(
			POST({
				locals: { user: developerUser },
				request: new Request('https://controller.test/api/plugins', {
					method: 'POST',
					body: form
				})
			} as Parameters<typeof POST>[0])
		).rejects.toMatchObject({ status: 400 });
	});

	it('rejects uploads when artifact hash mismatches manifest', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const artifactBuffer = Buffer.from('actual artifact', 'utf8');
		const manifestHash = '0'.repeat(64);

		const archiveBuffer = await createZipPluginPackage({
			'manifest.json': JSON.stringify({
				id: 'hash-mismatch',
				name: 'Hash Mismatch',
				version: '1.0.0',
				entry: 'mismatch.exe',
				requirements: {},
				distribution: {
					defaultMode: 'manual',
					autoUpdate: false,
					signature: 'sha256',
					signatureHash: manifestHash
				},
				package: {
					artifact: 'mismatch.bin',
					hash: manifestHash
				}
			}),
			'mismatch.bin': artifactBuffer
		});

		const form = new FormData();
		form.set('artifact', new File([toArrayBuffer(archiveBuffer)], 'hash-mismatch.zip'));

		await expect(
			POST({
				locals: { user: developerUser },
				request: new Request('https://controller.test/api/plugins', {
					method: 'POST',
					body: form
				})
			} as Parameters<typeof POST>[0])
		).rejects.toMatchObject({ status: 400 });
	});

	it('rejects uploads when signature verification fails', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const artifactBuffer = Buffer.from('unsigned artifact', 'utf8');
		const artifactHash = createHash('sha256').update(artifactBuffer).digest('hex');

		const archiveBuffer = await createZipPluginPackage({
			'manifest.json': JSON.stringify({
				id: 'signature-failure',
				name: 'Signature Failure',
				version: '1.0.0',
				entry: 'signature.exe',
				requirements: {},
				distribution: {
					defaultMode: 'manual',
					autoUpdate: false,
					signature: 'sha256',
					signatureHash: artifactHash
				},
				package: {
					artifact: 'signature.bin',
					hash: artifactHash
				}
			}),
			'signature.bin': artifactBuffer
		});

		const form = new FormData();
		form.set('artifact', new File([toArrayBuffer(archiveBuffer)], 'signature-failure.zip'));

		await expect(
			POST({
				locals: { user: developerUser },
				request: new Request('https://controller.test/api/plugins', {
					method: 'POST',
					body: form
				})
			} as Parameters<typeof POST>[0])
		).rejects.toMatchObject({ status: 400 });
	});

	it('rejects uploads with duplicate version numbers', async () => {
		const { POST } = await import('../src/routes/api/plugins/+server');

		const artifactBuffer = Buffer.from(artifactContent, 'utf8');
		const manifestHash = createHash('sha256').update(artifactBuffer).digest('hex');

		const archiveBuffer = await createZipPluginPackage({
			'manifest.json': JSON.stringify({
				id: manifestId,
				name: 'Test Plugin',
				version: '1.0.0',
				entry: 'plugin.exe',
				repositoryUrl: 'https://github.com/rootbay/test-plugin',
				license: { spdxId: 'MIT' },
				requirements: {},
				distribution: {
					defaultMode: 'automatic',
					autoUpdate: true,
					signature: 'sha256',
					signatureHash: manifestHash
				},
				package: {
					artifact: 'pkg.zip',
					hash: manifestHash,
					sizeBytes: artifactBuffer.byteLength
				}
			}),
			'pkg.zip': artifactBuffer
		});

		const form = new FormData();
		form.set('artifact', new File([toArrayBuffer(archiveBuffer)], 'pkg.zip'));

		await expect(
			POST({
				locals: { user: developerUser },
				request: new Request('https://controller.test/api/plugins', {
					method: 'POST',
					body: form
				})
			} as Parameters<typeof POST>[0])
		).rejects.toMatchObject({ status: 409 });
	});

	it('records manual stage requests for clients', async () => {
		const bootstrapStore = new PluginTelemetryStore();
		await bootstrapStore.getManifestSnapshot();
		const approvedAt = new Date();
		await db
			.update(pluginTable)
			.set({ approvalStatus: 'approved', approvedAt })
			.where(eq(pluginTable.id, manifestId));

		const stageModule = await import(
			'../src/routes/api/clients/[id]/plugins/[pluginId]/stage/+server'
		);

		const response = await stageModule.POST({
			params: { id: 'agent-1', pluginId: manifestId },
			request: new Request(
				'https://controller.test/api/clients/agent-1/plugins/test-plugin/stage',
				{
					method: 'POST'
				}
			)
		} as Parameters<typeof stageModule.POST>[0]);

		expect(getAgent).toHaveBeenCalledWith('agent-1');
		expect(response.status).toBe(200);

		const payload = (await response.json()) as { plugin: { id: string } };
		expect(payload.plugin.id).toBe(manifestId);

		const [pluginRow] = await db
			.select({ lastManualPushAt: pluginTable.lastManualPushAt })
			.from(pluginTable)
			.where(eq(pluginTable.id, manifestId));
		expect(pluginRow?.lastManualPushAt).toBeInstanceOf(Date);

		const verificationStore = new PluginTelemetryStore();
		const snapshot = await verificationStore.getManifestSnapshot();
		const descriptor = snapshot.manifests.find((entry) => entry.pluginId === manifestId);
		expect(descriptor?.manualPushAt).not.toBeNull();

		const delta = await verificationStore.getManifestDelta({
			digests: { [manifestId]: descriptor?.manifestDigest ?? '' }
		});
		expect(delta.updated.some((entry) => entry.pluginId === manifestId)).toBe(true);
	});
});
