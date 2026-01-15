import { describe, it, expect } from 'vitest';
import { generateSessionToken, hashVoucherCode, hashSessionToken } from './utils';
import { decodeBase64url, decodeHex } from '@oslojs/encoding';

describe('Auth Utils', () => {
	describe('generateSessionToken', () => {
		it('should generate a valid base64url string', () => {
			const token = generateSessionToken();
			expect(typeof token).toBe('string');
			expect(token.length).toBeGreaterThan(0);
			// Verify it can be decoded as base64url
			expect(() => decodeBase64url(token)).not.toThrow();
		});

		it('should generate unique tokens', () => {
			const token1 = generateSessionToken();
			const token2 = generateSessionToken();
			expect(token1).not.toBe(token2);
		});
	});

	describe('hashVoucherCode', () => {
		it('should hash a code correctly', () => {
			const code = 'test-code';
			const hash = hashVoucherCode(code);
			expect(typeof hash).toBe('string');
			// SHA256 hex string is 64 chars
			expect(hash).toHaveLength(64);
			expect(() => decodeHex(hash)).not.toThrow();
		});

		it('should normalize trimmed whitespace', () => {
			const code1 = 'test-code';
			const code2 = '  test-code  ';
			expect(hashVoucherCode(code1)).toBe(hashVoucherCode(code2));
		});

		it('should produce different hashes for different codes', () => {
			const hash1 = hashVoucherCode('code1');
			const hash2 = hashVoucherCode('code2');
			expect(hash1).not.toBe(hash2);
		});
	});

	describe('hashSessionToken', () => {
		it('should hash a token correctly', () => {
			const token = 'session-token';
			const hash = hashSessionToken(token);
			expect(typeof hash).toBe('string');
			expect(hash).toHaveLength(64);
		});

		it('should produce consistent hashes', () => {
			const token = 'consistent-token';
			expect(hashSessionToken(token)).toBe(hashSessionToken(token));
		});
	});
});
