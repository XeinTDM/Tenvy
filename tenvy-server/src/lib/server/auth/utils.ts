import { sha256 } from '@oslojs/crypto/sha2';
import { encodeBase64url, encodeHexLowerCase } from '@oslojs/encoding';

export function generateSessionToken() {
	const bytes = crypto.getRandomValues(new Uint8Array(18));
	const token = encodeBase64url(bytes);
	return token;
}

export function hashVoucherCode(code: string) {
	const normalized = code.trim();
	const digest = sha256(new TextEncoder().encode(normalized));
	return encodeHexLowerCase(digest);
}

export function hashSessionToken(token: string) {
	return encodeHexLowerCase(sha256(new TextEncoder().encode(token)));
}
