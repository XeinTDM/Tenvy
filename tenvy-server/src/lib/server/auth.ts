import type { RequestEvent } from '@sveltejs/kit';
import { eq } from 'drizzle-orm';
import { sha256 } from '@oslojs/crypto/sha2';
import { encodeBase64url, encodeHexLowerCase } from '@oslojs/encoding';
import { db } from '$lib/server/db';
import * as table from '$lib/server/db/schema';
import { logSystemEvent } from '$lib/server/audit';

const DAY_IN_MS = 1000 * 60 * 60 * 24;
const SHORT_SESSION_DURATION_MS = 1000 * 60 * 10; // 10 minutes for onboarding
const LONG_SESSION_DURATION_MS = DAY_IN_MS * 30;

export const sessionCookieName = 'auth-session';

export function generateSessionToken() {
	const bytes = crypto.getRandomValues(new Uint8Array(18));
	const token = encodeBase64url(bytes);
	return token;
}

export async function createSession(
	token: string,
	userId: string,
	{
		type = 'long',
		expiresInMs,
		description
	}: { type?: 'long' | 'short'; expiresInMs?: number; description?: string } = {}
) {
	const sessionId = encodeHexLowerCase(sha256(new TextEncoder().encode(token)));
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
	const sessionId = encodeHexLowerCase(sha256(new TextEncoder().encode(token)));
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

export function setSessionTokenCookie(event: RequestEvent, token: string, expiresAt: Date | null) {
	event.cookies.set(sessionCookieName, token, {
		expires: expiresAt ?? undefined,
		httpOnly: true,
		sameSite: 'strict',
		secure: event.url.protocol === 'https:',
		path: '/'
	});
}

export function deleteSessionTokenCookie(event: RequestEvent) {
	event.cookies.delete(sessionCookieName, {
		path: '/',
		httpOnly: true,
		sameSite: 'strict',
		secure: event.url.protocol === 'https:'
	});
}

export function hashVoucherCode(code: string) {
	const normalized = code.trim();
	const digest = sha256(new TextEncoder().encode(normalized));
	return encodeHexLowerCase(digest);
}

export type UserRole = 'viewer' | 'operator' | 'developer' | 'admin';

export type AuthenticatedUser = {
	id: string;
	role: UserRole;
	passkeyRegistered: boolean;
	voucherId: string;
	voucherActive: boolean;
	voucherExpiresAt: Date | null;
};

export const sessionDurations = {
	short: SHORT_SESSION_DURATION_MS,
	long: LONG_SESSION_DURATION_MS
};
