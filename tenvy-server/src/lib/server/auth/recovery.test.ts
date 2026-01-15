import { describe, it, expect, vi, beforeEach } from 'vitest';
import { issueRecoveryCodes } from './recovery';
import { db } from '$lib/server/db';
import * as table from '$lib/server/db/schema';

vi.mock('$lib/server/db', () => ({
	db: {
		transaction: vi.fn()
	}
}));

describe('Recovery Codes', () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	it('should issue recovery codes and hash them', async () => {
		const userId = 'user-1';
		const count = 5;
		const mockTx = {
			delete: vi.fn().mockReturnThis(),
			where: vi.fn().mockReturnThis(),
			run: vi.fn().mockReturnThis(),
			insert: vi.fn().mockReturnThis(),
			values: vi.fn().mockReturnThis()
		};

		(db.transaction as any).mockImplementation((cb: any) => cb(mockTx));

		const codes = await issueRecoveryCodes(userId, count);

		expect(codes).toHaveLength(count);
		codes.forEach(code => {
			expect(code).toMatch(/^[A-Z2-9]{5}-[A-Z2-9]{5}-[A-Z2-9]{5}-[A-Z2-9]{5}$/);
		});

		expect(mockTx.delete).toHaveBeenCalledWith(table.recoveryCode);
		expect(mockTx.where).toHaveBeenCalled();
		expect(mockTx.insert).toHaveBeenCalledWith(table.recoveryCode);
	});

	it('should handle zero codes if requested', async () => {
		const userId = 'user-1';
		const mockTx = {
			delete: vi.fn().mockReturnThis(),
			where: vi.fn().mockReturnThis(),
			run: vi.fn().mockReturnThis(),
			insert: vi.fn().mockReturnThis()
		};
		(db.transaction as any).mockImplementation((cb: any) => cb(mockTx));

		const codes = await issueRecoveryCodes(userId, 0);

		expect(codes).toHaveLength(0);
		expect(mockTx.delete).toHaveBeenCalled();
		expect(mockTx.insert).not.toHaveBeenCalled();
	});
});
