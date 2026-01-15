import { eq } from 'drizzle-orm';
import { db } from '$lib/server/db';
import * as table from '$lib/server/db/schema';
import { logSystemEvent } from '$lib/server/audit';
import { hashSessionToken } from './utils';
import type { AuthenticatedUser, UserRole } from './types';

const DAY_IN_MS = 1000 * 60 * 60 * 24;
const SHORT_SESSION_DURATION_MS = 1000 * 60 * 10;
const LONG_SESSION_DURATION_MS = DAY_IN_MS * 30;

export const sessionDurations = {
	short: SHORT_SESSION_DURATION_MS,
	long: LONG_SESSION_DURATION_MS
};

export async function createSession(
	token: string,
	userId: string,
	{
		type = 'long',
		expiresInMs,
		description
	}: { type?: 'long' | 'short'; expiresInMs?: number; description?: string } = {}
) {
	const sessionId = hashSessionToken(token);
	const now = Date.now();
	const resolvedType = type;
	const ttl =
		expiresInMs ??
		(resolvedType === 'short' ? SHORT_SESSION_DURATION_MS : LONG_SESSION_DURATION_MS);
	const session: table.Session = {
		id: sessionId,
		userId,
		expiresAt: new Date(now + ttl),
		createdAt: new Date(now),
		description: description ?? resolvedType
	};
	await db.insert(table.session).values(session);
	await logSystemEvent('auth.login', { sessionType: resolvedType }, userId);
	return session;
}

export async function validateSessionToken(token: string) {
	const sessionId = hashSessionToken(token);
	const [result] = await db
		.select({
			user: {
				id: table.user.id,
				role: table.user.role,
				passkeyRegistered: table.user.passkeyRegistered,
				voucherId: table.user.voucherId
			},
			voucher: {
				id: table.voucher.id,
				expiresAt: table.voucher.expiresAt,
				revokedAt: table.voucher.revokedAt
			},
			session: table.session
		})
		.from(table.session)
		.innerJoin(table.user, eq(table.session.userId, table.user.id))
		.innerJoin(table.voucher, eq(table.user.voucherId, table.voucher.id))
		.where(eq(table.session.id, sessionId));

	if (!result) {
		return { session: null, user: null };
	}
	const { session, user, voucher } = result;

	if (!session.expiresAt) {
		await db.delete(table.session).where(eq(table.session.id, session.id));
		return { session: null, user: null };
	}

	const sessionExpired = Date.now() >= session.expiresAt.getTime();
	if (sessionExpired) {
		await db.delete(table.session).where(eq(table.session.id, session.id));
		return { session: null, user: null };
	}

	const renewSession =
		(session.description ?? 'long') !== 'short' &&
		Date.now() >= session.expiresAt.getTime() - DAY_IN_MS * 15;
	if (renewSession) {
		session.expiresAt = new Date(Date.now() + LONG_SESSION_DURATION_MS);
		await db
			.update(table.session)
			.set({ expiresAt: session.expiresAt })
			.where(eq(table.session.id, session.id));
	}

	const voucherActive =
		!voucher.revokedAt && (!voucher.expiresAt || voucher.expiresAt.getTime() > Date.now());

	if (!voucherActive) {
		await db.delete(table.session).where(eq(table.session.id, session.id));
		return { session: null, user: null };
	}

	const sanitizedUser = {
		id: user.id,
		role: user.role as UserRole,
		passkeyRegistered: Boolean(user.passkeyRegistered),
		voucherId: user.voucherId,
		voucherActive,
		voucherExpiresAt: voucher.expiresAt ?? null
	} satisfies AuthenticatedUser;

	return { session, user: sanitizedUser };
}

export type SessionValidationResult = Awaited<ReturnType<typeof validateSessionToken>>;

export async function invalidateSession(sessionId: string) {
	await db.delete(table.session).where(eq(table.session.id, sessionId));
}
