import { describe, it, expect } from 'vitest';
import { 
	computeFingerprint, 
	normalizeTags, 
	mergeRecentResults, 
	parseNumeric,
	ensureMetadata,
	hashAgentKey
} from './utils';
import type { AgentMetadata } from '../../../../../shared/types/agent';

describe('RAT Utils', () => {
	describe('computeFingerprint', () => {
		it('should produce a consistent hex hash', () => {
			const metadata: AgentMetadata = {
				hostname: 'TEST-PC',
				username: 'admin',
				os: 'windows',
				architecture: 'x64',
				group: 'default',
				hardwareId: 'hw-123',
				ipAddress: '127.0.0.1',
				publicIpAddress: '127.0.0.1',
				version: '1.0.0'
			};
			const fp1 = computeFingerprint(metadata);
			const fp2 = computeFingerprint({ ...metadata, hostname: ' test-pc ' });
			expect(fp1).toHaveLength(64);
			expect(fp1).toBe(fp2);
		});
	});

	describe('normalizeTags', () => {
		it('should trim, deduplicate and filter tags', () => {
			const input = [' tag1 ', 'TAG1', '!!invalid!!', 'good-tag', '  ', 'a'.repeat(50)];
			const result = normalizeTags(input);
			expect(result).toEqual(['tag1', 'good-tag']);
		});

		it('should limit the number of tags', () => {
			const input = Array.from({ length: 20 }, (_, i) => `tag${i}`);
			const result = normalizeTags(input);
			expect(result).toHaveLength(16); // MAX_TAGS
		});
	});

	describe('mergeRecentResults', () => {
		it('should merge and sort results by timestamp desc', () => {
			const existing = [
				{ commandId: 'c1', success: true, completedAt: '2025-01-01T10:00:00Z' }
			];
			const incoming = [
				{ commandId: 'c2', success: true, completedAt: '2025-01-01T11:00:00Z' },
				{ commandId: 'c1', success: false, completedAt: '2025-01-01T12:00:00Z' }
			];
			
			const merged = mergeRecentResults(existing as any, incoming as any, 10);
			expect(merged).toHaveLength(2);
			expect(merged[0].commandId).toBe('c1');
			expect(merged[0].success).toBe(false);
			expect(merged[1].commandId).toBe('c2');
		});
	});

	describe('parseNumeric', () => {
		it('should parse various numeric inputs', () => {
			expect(parseNumeric(123)).toBe(123);
			expect(parseNumeric('456')).toBe(456);
			expect(parseNumeric('abc')).toBeNull();
			expect(parseNumeric('')).toBeNull();
			expect(parseNumeric(null)).toBeNull();
		});
	});

	describe('ensureMetadata', () => {
		it('should fill missing IP addresses', () => {
			const metadata: AgentMetadata = {
				hostname: 'h', username: 'u', os: 'o', architecture: 'a',
				group: 'g', hardwareId: 'hw', version: 'v'
			};
			const result = ensureMetadata(metadata, '1.2.3.4');
			expect(result.ipAddress).toBe('1.2.3.4');
			expect(result.publicIpAddress).toBe('1.2.3.4');
		});
	});
});
