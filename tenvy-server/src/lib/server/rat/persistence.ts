import { eq, inArray } from 'drizzle-orm';
import { db } from '$lib/server/db';
import {
	agent as agentTable,
	agentNote as agentNoteTable,
	agentCommand as agentCommandTable,
	agentResult as agentResultTable
} from '$lib/server/db/schema';
import type {
	AgentMetadata,
	AgentMetrics,
	AgentStatus
} from '../../../../../shared/types/agent';
import {
	defaultAgentConfig,
	type AgentConfig,
} from '../../../../../shared/types/config';
import type {
	OptionsState
} from '../../../../../shared/types/options';
import type { Command, CommandResult } from '../../../../../shared/types/messages';
import {
	downloadCatalogueSchema,
	type DownloadCatalogue
} from '$lib/types/downloads';
import type { AgentRecord, SharedNoteRecord, OperatorNoteRecord } from './types';
import * as utils from './utils';

const { MAX_RECENT_RESULTS } = utils;

export class AgentPersistence {
	async persistAgents(agents: AgentRecord[]): Promise<void> {
		if (agents.length === 0) {
			return;
		}

		const now = new Date();
		const agentIds = agents.map((agent) => agent.id);

		db.transaction((tx) => {
			const existing = tx
				.select({ id: agentTable.id })
				.from(agentTable)
				.where(inArray(agentTable.id, agentIds))
				.all();
			const existingIds = new Set(existing.map((row) => row.id));

			for (const record of agents) {
				const payload = {
					id: record.id,
					keyHash: record.keyHash,
					metadata: JSON.stringify(record.metadata),
					status: record.status,
					connectedAt: record.connectedAt,
					lastSeen: record.lastSeen,
					metrics: record.metrics ? JSON.stringify(record.metrics) : null,
					config: JSON.stringify(record.config),
					optionsState: record.optionsState ? JSON.stringify(record.optionsState) : null,
					downloadsCatalogue:
						record.downloadsCatalogue.length > 0 ? JSON.stringify(record.downloadsCatalogue) : null,
					operatorNote: record.operatorNote ? record.operatorNote.note : null,
					operatorNoteTags: record.operatorNote ? JSON.stringify(record.operatorNote.tags) : null,
					operatorNoteUpdatedAt: record.operatorNote?.updatedAt ?? null,
					operatorNoteUpdatedBy: record.operatorNote?.updatedBy ?? null,
					fingerprint: record.fingerprint,
					sharedSecret: record.sharedSecret ? utils.encryptDatabaseField(record.sharedSecret) : null,
					createdAt: record.connectedAt,
					updatedAt: now
				};

				if (existingIds.has(record.id)) {
					tx.update(agentTable)
						.set({
							keyHash: payload.keyHash,
							metadata: payload.metadata,
							status: payload.status,
							connectedAt: payload.connectedAt,
							lastSeen: payload.lastSeen,
							metrics: payload.metrics,
							config: payload.config,
							downloadsCatalogue: payload.downloadsCatalogue,
							optionsState: payload.optionsState,
							operatorNote: payload.operatorNote,
							operatorNoteTags: payload.operatorNoteTags,
							operatorNoteUpdatedAt: payload.operatorNoteUpdatedAt,
							operatorNoteUpdatedBy: payload.operatorNoteUpdatedBy,
							fingerprint: payload.fingerprint,
							sharedSecret: payload.sharedSecret,
							updatedAt: payload.updatedAt
						})
						.where(eq(agentTable.id, record.id))
						.run();
				} else {
					tx.insert(agentTable).values(payload).run();
					existingIds.add(record.id);
				}

				tx.delete(agentNoteTable).where(eq(agentNoteTable.agentId, record.id)).run();
				const notes = Array.from(record.sharedNotes.values());
				if (notes.length > 0) {
					tx.insert(agentNoteTable)
						.values(
							notes.map((note) => ({
								agentId: record.id,
								noteId: note.id,
								ciphertext: note.ciphertext,
								nonce: note.nonce,
								digest: note.digest,
								version: note.version,
								updatedAt: note.updatedAt
							}))
						)
						.run();
				}

				tx.delete(agentCommandTable).where(eq(agentCommandTable.agentId, record.id)).run();
				if (record.pendingCommands.length > 0) {
					tx.insert(agentCommandTable)
						.values(
							record.pendingCommands.map((command) => ({
								id: command.id,
								agentId: record.id,
								name: command.name,
								payload: JSON.stringify(command.payload ?? {}),
								createdAt: new Date(command.createdAt)
							}))
						)
						.run();
				}

				tx.delete(agentResultTable).where(eq(agentResultTable.agentId, record.id)).run();
				if (record.recentResults.length > 0) {
					tx.insert(agentResultTable)
						.values(
							record.recentResults.map((result) => ({
								agentId: record.id,
								commandId: result.commandId,
								success: result.success,
								output: result.output,
								error: result.error,
								completedAt: new Date(result.completedAt)
							}))
						)
						.run();
				}

				record.dirty = false;
			}
		});
	}

	loadAllAgents(): AgentRecord[] {
		let agentRows: Array<typeof agentTable.$inferSelect> = [];
		try {
			agentRows = db.select().from(agentTable).all();
		} catch (error) {
			console.error('Failed to read agent records from database', error);
			return [];
		}

		const noteRows = db.select().from(agentNoteTable).all();
		const commandRows = db.select().from(agentCommandTable).orderBy(agentCommandTable.createdAt).all();
		const resultRows = db.select().from(agentResultTable).orderBy(agentResultTable.completedAt).all();

		const notesByAgent = new Map<string, Map<string, SharedNoteRecord>>();
		for (const row of noteRows) {
			const updatedAt = row.updatedAt instanceof Date ? row.updatedAt : new Date(row.updatedAt ?? Date.now());
			if (!notesByAgent.has(row.agentId)) {
				notesByAgent.set(row.agentId, new Map());
			}
			notesByAgent.get(row.agentId)!.set(row.noteId, {
				id: row.noteId,
				ciphertext: row.ciphertext,
				nonce: row.nonce,
				digest: row.digest,
				version: row.version ?? 1,
				updatedAt
			});
		}

		const commandsByAgent = new Map<string, Command[]>();
		for (const row of commandRows) {
			let payload: Command['payload'];
			try {
				payload = row.payload ? JSON.parse(row.payload) : {};
			} catch {
				payload = {};
			}
			if (!commandsByAgent.has(row.agentId)) {
				commandsByAgent.set(row.agentId, []);
			}
			commandsByAgent.get(row.agentId)!.push({
				id: row.id,
				name: row.name as Command['name'],
				payload,
				createdAt: (row.createdAt instanceof Date ? row.createdAt : new Date(row.createdAt ?? Date.now())).toISOString()
			});
		}

		const resultsByAgent = new Map<string, CommandResult[]>();
		for (const row of resultRows) {
			if (!resultsByAgent.has(row.agentId)) {
				resultsByAgent.set(row.agentId, []);
			}
			resultsByAgent.get(row.agentId)!.push({
				commandId: row.commandId,
				success: Boolean(row.success),
				output: row.output ?? undefined,
				error: row.error ?? undefined,
				completedAt: (row.completedAt instanceof Date ? row.completedAt : new Date(row.completedAt ?? Date.now())).toISOString()
			});
		}

		return agentRows
			.map((row) => {
				try {
					return this.rowToRecord(
						row,
						notesByAgent.get(row.id) ?? new Map(),
						commandsByAgent.get(row.id) ?? [],
						resultsByAgent.get(row.id) ?? []
					);
				} catch (error) {
					console.error(`Failed to load agent ${row.id}:`, error);
					return null;
				}
			})
			.filter((a): a is AgentRecord => a !== null);
	}

	loadAgentById(id: string): AgentRecord | null {
		const row = db.select().from(agentTable).where(eq(agentTable.id, id)).get();
		if (!row) return null;

		const noteRows = db.select().from(agentNoteTable).where(eq(agentNoteTable.agentId, id)).all();
		const commandRows = db.select().from(agentCommandTable).where(eq(agentCommandTable.agentId, id)).orderBy(agentCommandTable.createdAt).all();
		const resultRows = db.select().from(agentResultTable).where(eq(agentResultTable.agentId, id)).orderBy(agentResultTable.completedAt).all();

		const sharedNotes = new Map<string, SharedNoteRecord>();
		for (const nr of noteRows) {
			sharedNotes.set(nr.noteId, {
				id: nr.noteId,
				ciphertext: nr.ciphertext,
				nonce: nr.nonce,
				digest: nr.digest,
				version: nr.version ?? 1,
				updatedAt: nr.updatedAt instanceof Date ? nr.updatedAt : new Date(nr.updatedAt ?? Date.now())
			});
		}

		const pendingCommands: Command[] = commandRows.map((cr) => ({
			id: cr.id,
			name: cr.name as Command['name'],
			payload: cr.payload ? JSON.parse(cr.payload) : {},
			createdAt: (cr.createdAt instanceof Date ? cr.createdAt : new Date(cr.createdAt ?? Date.now())).toISOString()
		}));

		const recentResults = utils.mergeRecentResults(
			[],
			resultRows.map((rr) => ({
				commandId: rr.commandId,
				success: Boolean(rr.success),
				output: rr.output ?? undefined,
				error: rr.error ?? undefined,
				completedAt: (rr.completedAt instanceof Date ? rr.completedAt : new Date(rr.completedAt ?? Date.now())).toISOString()
			})),
			MAX_RECENT_RESULTS
		);

		return this.rowToRecord(row, sharedNotes, pendingCommands, recentResults);
	}

	loadAgentByFingerprint(fingerprint: string): AgentRecord | null {
		const row = db.select().from(agentTable).where(eq(agentTable.fingerprint, fingerprint)).get();
		if (!row) return null;
		return this.loadAgentById(row.id);
	}

	private rowToRecord(
		row: typeof agentTable.$inferSelect,
		sharedNotes: Map<string, SharedNoteRecord>,
		pendingCommands: Command[],
		results: CommandResult[]
	): AgentRecord {
		let metadata: AgentMetadata | null = null;
		try {
			metadata = JSON.parse(row.metadata) as AgentMetadata;
		} catch {
			throw new Error(`Invalid metadata for agent ${row.id}`);
		}

		let config: AgentConfig = utils.normalizeConfig(null);
		if (row.config) {
			try {
				config = utils.normalizeConfig(JSON.parse(row.config) as Partial<AgentConfig>);
			} catch {
				// ignore
			}
		}

		let metrics: AgentMetrics | undefined;
		if (row.metrics) {
			try {
				metrics = JSON.parse(row.metrics) as AgentMetrics;
			} catch {
				// ignore
			}
		}

		let optionsState: OptionsState | null = null;
		if (row.optionsState) {
			try {
				optionsState = utils.cloneOptionsState(JSON.parse(row.optionsState) as OptionsState);
			} catch {
				// ignore
			}
		}

		let downloadsCatalogue: DownloadCatalogue = [];
		if (row.downloadsCatalogue) {
			try {
				downloadsCatalogue = downloadCatalogueSchema.parse(JSON.parse(row.downloadsCatalogue));
			} catch {
				// ignore
			}
		}

		let operatorNote: OperatorNoteRecord | null = null;
		if (row.operatorNote !== null) {
			let tags: string[] = [];
			try {
				tags = row.operatorNoteTags ? JSON.parse(row.operatorNoteTags) : [];
			} catch {
				// ignore
			}
			operatorNote = {
				note: row.operatorNote,
				tags,
				updatedAt: row.operatorNoteUpdatedAt ? new Date(row.operatorNoteUpdatedAt) : null,
				updatedBy: row.operatorNoteUpdatedBy ?? null
			};
		}

		const connectedAt = row.connectedAt instanceof Date ? row.connectedAt : new Date(row.connectedAt ?? Date.now());
		const lastSeen = row.lastSeen instanceof Date ? row.lastSeen : new Date(row.lastSeen ?? connectedAt);

		return {
			id: row.id,
			keyHash: row.keyHash,
			metadata: {
				...metadata,
				tags: utils.normalizeTags(metadata.tags ?? [])
			},
			status: row.status as AgentStatus,
			connectedAt,
			lastSeen,
			metrics,
			config,
			pendingCommands,
			recentResults: utils.mergeRecentResults([], results, MAX_RECENT_RESULTS),
			sharedNotes,
			operatorNote,
			fingerprint: row.fingerprint,
			sharedSecret: row.sharedSecret ? (utils.decryptDatabaseField(row.sharedSecret) ?? undefined) : undefined,
			optionsState,
			downloadsCatalogue
		};
	}
}
