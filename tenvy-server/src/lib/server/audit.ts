import { db } from '$lib/server/db';
import { systemAuditEvent } from '$lib/server/db/schema';

export async function logSystemEvent(
	action: string,
	details: Record<string, unknown>,
	actorId?: string | null,
	targetId?: string | null
) {
	try {
		await db.insert(systemAuditEvent).values({
			action,
			details: JSON.stringify(details),
			actorId: actorId ?? null,
			targetId: targetId ?? null
		});
	} catch (e) {
		console.error('Failed to log system audit event', e);
	}
}
