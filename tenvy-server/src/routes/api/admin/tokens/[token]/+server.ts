import { json } from '@sveltejs/kit';
import { db } from '$lib/server/db';
import { enrollmentToken } from '$lib/server/db/schema';
import { eq } from 'drizzle-orm';

export async function DELETE({ locals, params }) {
	if (!locals.user) {
		return json({ error: 'Unauthorized' }, { status: 401 });
	}

	if (locals.user.role !== 'admin' && locals.user.role !== 'operator') {
		return json({ error: 'Forbidden' }, { status: 403 });
	}

	const token = params.token;

	try {
		db.update(enrollmentToken)
			.set({ revokedAt: new Date() })
			.where(eq(enrollmentToken.token, token))
			.run();

		return json({ success: true });
	} catch (error) {
		console.error('Failed to revoke enrollment token', error);
		return json({ error: 'Internal Server Error' }, { status: 500 });
	}
}
