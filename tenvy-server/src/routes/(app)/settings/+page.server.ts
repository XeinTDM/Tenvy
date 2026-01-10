import type { PageServerLoad } from './$types';
import { requireOperator } from '$lib/server/authorization.js';
import { db } from '$lib/server/db/index.js';
import { voucher, user } from '$lib/server/db/schema.js';
import { eq } from 'drizzle-orm';

export const load: PageServerLoad = async ({ locals }) => {
	const operator = requireOperator(locals.user);
	const records = await db
		.select({
			id: user.id,
			role: user.role,
			voucherId: user.voucherId,
			createdAt: user.createdAt,
			voucherExpiresAt: voucher.expiresAt,
			voucherRedeemedAt: voucher.redeemedAt
		})
		.from(user)
		.innerJoin(voucher, eq(user.voucherId, voucher.id))
		.orderBy(user.createdAt);

	return {
		user: operator,
		members: records.map((record) => {
			if (!record.createdAt) {
				throw new Error('User record missing creation timestamp');
			}

			return {
				...record,
				createdAt: record.createdAt.toISOString(),
				voucherExpiresAt: record.voucherExpiresAt ? record.voucherExpiresAt.toISOString() : null,
				voucherRedeemedAt: record.voucherRedeemedAt ? record.voucherRedeemedAt.toISOString() : null
			};
		})
	};
};
