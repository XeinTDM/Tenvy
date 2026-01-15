import type { RequestEvent } from '@sveltejs/kit';

export const sessionCookieName = 'auth-session';

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
