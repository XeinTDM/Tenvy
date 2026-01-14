import { eq, inArray } from 'drizzle-orm';
import { db } from '$lib/server/db';
import {
	agent as agentTable,
	agentNote as agentNoteTable,
	agentCommand as agentCommandTable,
	agentResult as agentResultTable
} from '$lib/server/db/schema';
import type { AgentRecord } from './types';
import { MAX_PENDING_COMMANDS } from './utils';

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
}
