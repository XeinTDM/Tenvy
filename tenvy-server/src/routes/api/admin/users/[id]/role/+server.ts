import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { eq } from 'drizzle-orm';
import { db } from '$lib/server/db/index.js';
import * as table from '$lib/server/db/schema.js';
import { requireAdmin } from '$lib/server/authorization.js';
import type { UserRole } from '$lib/server/auth.js';
import { logSystemEvent } from '$lib/server/audit.js';

const allowedRoles: UserRole[] = ['viewer', 'operator', 'developer', 'admin'];

export const PATCH: RequestHandler = async ({ params, request, locals }) => {
	requireAdmin(locals.user);

	const { role } = (await request.json()) as { role?: UserRole };
	if (!role || !allowedRoles.includes(role)) {
		throw error(400, 'role is required');
	}

	const targetId = params.id;
	const result = await db.update(table.user).set({ role }).where(eq(table.user.id, targetId));

	if (result.changes === 0) {
		throw error(404, 'User not found');
	}

	await logSystemEvent(
		'user.update_role',
		{ role },
		locals.user.id,
		targetId
	);

	const [updated] = await db
		.select({
			id: table.user.id,
			role: table.user.role,
			voucherId: table.user.voucherId,
			createdAt: table.user.createdAt
		})
		.from(table.user)
		.where(eq(table.user.id, targetId))
		.limit(1);

	return json({ user: updated });
};
