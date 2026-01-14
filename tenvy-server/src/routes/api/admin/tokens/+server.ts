import { json } from '@sveltejs/kit';
import { db } from '$lib/server/db';
import { enrollmentToken } from '$lib/server/db/schema';
import { randomBytes } from 'crypto';

export async function POST({ locals, request }) {
	if (!locals.user) {
		return json({ error: 'Unauthorized' }, { status: 401 });
	}

	if (locals.user.role !== 'admin' && locals.user.role !== 'operator') {
		return json({ error: 'Forbidden' }, { status: 403 });
	}

	try {
		const body = await request.json();
		const memo = body.memo || null;
		const maxUses = body.maxUses || 1;
		const expiresInHours = body.expiresInHours || 24;

		const token = randomBytes(32).toString('hex');
		const expiresAt = new Date(Date.now() + expiresInHours * 60 * 60 * 1000);

		db.insert(enrollmentToken)
			.values({
				token,
				createdBy: locals.user.id,
				maxUses,
				expiresAt,
				memo
			})
			.run();

		return json({ token, expiresAt });
	} catch (error) {
		console.error('Failed to create enrollment token', error);
		return json({ error: 'Internal Server Error' }, { status: 500 });
	}
}
