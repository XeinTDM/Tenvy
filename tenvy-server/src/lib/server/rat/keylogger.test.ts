import { beforeEach, describe, expect, it } from 'vitest';
import type { KeyloggerEventEnvelope } from '$lib/types/keylogger';
import { KeyloggerManager } from './keylogger';
import { db } from '$lib/server/db';
import { agent as agentTable } from '$lib/server/db/schema';

describe('KeyloggerManager', () => {
	let manager: KeyloggerManager;
	const agentId = 'agent-1';

	beforeEach(() => {
		db.delete(agentTable).run();
		db.insert(agentTable)
			.values({
				id: agentId,
				keyHash: 'hash',
				metadata: JSON.stringify({ hostname: 'test' }),
				status: 'online',
				connectedAt: new Date(),
				lastSeen: new Date(),
				config: JSON.stringify({}),
				fingerprint: 'fingerprint',
				createdAt: new Date(),
				updatedAt: new Date()
			})
			.run();
		manager = new KeyloggerManager();
	});

	it('creates sessions with normalized configuration', async () => {
		const session = await manager.createSession(agentId, {
			mode: 'offline',
			batchIntervalMs: 60_000,
			includeClipboard: true
		});

		expect(session.sessionId).toBeTruthy();
		expect(session.agentId).toBe(agentId);
		expect(session.mode).toBe('offline');
		expect(session.config.bufferSize).toBeGreaterThan(0);

		const state = await manager.getState(agentId);
		expect(state.session?.active).toBe(true);
		expect(state.telemetry.totalEvents).toBe(0);
	});

	it('ingests keylogger telemetry batches', async () => {
		const session = await manager.createSession(agentId, { mode: 'standard', cadenceMs: 100 });

		const capturedAt = new Date();
		capturedAt.setMilliseconds(0);
		const capturedAtIso = capturedAt.toISOString();

		const envelope: KeyloggerEventEnvelope = {
			sessionId: session.sessionId,
			mode: 'standard',
			capturedAt: capturedAtIso,
			batchId: 'batch-1',
			events: [
				{
					sequence: 1,
					capturedAt: capturedAtIso,
					key: 'a',
					text: 'test'
				}
			],
			totalEvents: 1
		} satisfies KeyloggerEventEnvelope;

		const telemetry = await manager.ingest(agentId, envelope);
		expect(telemetry.totalEvents).toBe(1);
		expect(telemetry.batches).toHaveLength(1);
		expect(telemetry.batches[0].batchId).toBe('batch-1');

		const state = await manager.getState(agentId);
		expect(state.session?.totalEvents).toBe(1);
		expect(state.session?.lastCapturedAt).toBe(envelope.capturedAt);
	});

	it('marks sessions inactive when stopped', async () => {
		const session = await manager.createSession(agentId, { mode: 'standard' });
		const stopped = await manager.stopSession(agentId, session.sessionId);
		expect(stopped?.active).toBe(false);
	});
});
