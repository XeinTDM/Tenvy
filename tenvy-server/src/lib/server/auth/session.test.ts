import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createSession, validateSessionToken, invalidateSession, sessionDurations } from './session';
import { db } from '$lib/server/db';
import * as table from '$lib/server/db/schema';
import { logSystemEvent } from '$lib/server/audit';

vi.mock('$lib/server/db', () => ({
	db: {
		insert: vi.fn(),
		select: vi.fn(),
		delete: vi.fn(),
		update: vi.fn()
	}
}));

vi.mock('$lib/server/audit', () => ({
	logSystemEvent: vi.fn()
}));

const mockDb = db as unknown as {
	insert: ReturnType<typeof vi.fn>;
	select: ReturnType<typeof vi.fn>;
	delete: ReturnType<typeof vi.fn>;
	update: ReturnType<typeof vi.fn>;
};

describe('Auth Session', () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	describe('createSession', () => {
		it('should create a long session by default', async () => {
			const valuesMock = vi.fn().mockResolvedValue(undefined);
			mockDb.insert.mockReturnValue({ values: valuesMock });

			const userId = 'user-123';
			const token = 'test-token';
			const session = await createSession(token, userId);

			expect(mockDb.insert).toHaveBeenCalledWith(table.session);
			expect(valuesMock).toHaveBeenCalledWith(expect.objectContaining({
				userId,
				description: 'long'
			}));
			const ttl = sessionDurations.long;
			const expectedExpiry = Date.now() + ttl;
			expect(session.expiresAt!.getTime()).toBeGreaterThan(expectedExpiry - 10000);
			expect(session.expiresAt!.getTime()).toBeLessThan(expectedExpiry + 10000);

			expect(logSystemEvent).toHaveBeenCalledWith('auth.login', { sessionType: 'long' }, userId);
		});

		it('should create a short session when requested', async () => {
			const valuesMock = vi.fn().mockResolvedValue(undefined);
			mockDb.insert.mockReturnValue({ values: valuesMock });

			const userId = 'user-123';
			const token = 'test-token';
			const session = await createSession(token, userId, { type: 'short' });

			expect(valuesMock).toHaveBeenCalledWith(expect.objectContaining({
				userId,
				description: 'short'
			}));
			const ttl = sessionDurations.short;
			const expectedExpiry = Date.now() + ttl;
			expect(session.expiresAt!.getTime()).toBeGreaterThan(expectedExpiry - 10000);
			expect(session.expiresAt!.getTime()).toBeLessThan(expectedExpiry + 10000);

			expect(logSystemEvent).toHaveBeenCalledWith('auth.login', { sessionType: 'short' }, userId);
		});
	});

	describe('validateSessionToken', () => {
		it('should return null if session not found', async () => {
			const whereMock = vi.fn().mockResolvedValue([]);
			const innerJoinMock2 = vi.fn().mockReturnValue({ where: whereMock });
			const innerJoinMock1 = vi.fn().mockReturnValue({ innerJoin: innerJoinMock2 });
			const fromMock = vi.fn().mockReturnValue({ innerJoin: innerJoinMock1 });
			mockDb.select.mockReturnValue({ from: fromMock });

			const result = await validateSessionToken('invalid-token');
			expect(result).toEqual({ session: null, user: null });
		});

		it('should return session and user if valid', async () => {
			const now = new Date();
			const expiresAt = new Date(now.getTime() + 1728000000); 
			const mockSession = {
				id: 'session-id',
				userId: 'user-id',
				expiresAt: expiresAt,
				createdAt: now,
				description: 'long'
			};
			const mockUser = {
				id: 'user-id',
				role: 'operator',
				passkeyRegistered: 0,
				voucherId: 'voucher-id'
			};
			const mockVoucher = {
				id: 'voucher-id',
				expiresAt: new Date(now.getTime() + 2000000000),
				revokedAt: null
			};

			const whereMock = vi.fn().mockResolvedValue([{
				session: mockSession,
				user: mockUser,
				voucher: mockVoucher
			}]);
			
			const innerJoinMock2 = vi.fn().mockReturnValue({ where: whereMock });
			const innerJoinMock1 = vi.fn().mockReturnValue({ innerJoin: innerJoinMock2 });
			const fromMock = vi.fn().mockReturnValue({ innerJoin: innerJoinMock1 });
			mockDb.select.mockReturnValue({ from: fromMock });

			const updateWhereMock = vi.fn().mockResolvedValue(undefined);
			const setMock = vi.fn().mockReturnValue({ where: updateWhereMock });
			mockDb.update.mockReturnValue({ set: setMock });

			const result = await validateSessionToken('valid-token');
			
			expect(result.session).toEqual(mockSession);
			expect(result.user).toEqual({
				id: mockUser.id,
				role: mockUser.role,
				passkeyRegistered: false,
				voucherId: mockUser.voucherId,
				voucherActive: true,
				voucherExpiresAt: mockVoucher.expiresAt
			});
		});

        it('should delete session if expired', async () => {
            const now = new Date();
            const expiresAt = new Date(now.getTime() - 1000);
            const mockSession = {
                id: 'session-id',
                userId: 'user-id',
                expiresAt: expiresAt,
                createdAt: now,
                description: 'long'
            };
            const mockUser = { id: 'user-id', role: 'operator', passkeyRegistered: 0, voucherId: 'v-id' };
            const mockVoucher = { id: 'v-id', expiresAt: null, revokedAt: null };

            const whereMock = vi.fn().mockResolvedValue([{
                session: mockSession,
                user: mockUser,
                voucher: mockVoucher
            }]);
            
            const innerJoinMock2 = vi.fn().mockReturnValue({ where: whereMock });
            const innerJoinMock1 = vi.fn().mockReturnValue({ innerJoin: innerJoinMock2 });
            const fromMock = vi.fn().mockReturnValue({ innerJoin: innerJoinMock1 });
            mockDb.select.mockReturnValue({ from: fromMock });

            const deleteWhereMock = vi.fn().mockResolvedValue(undefined);
            mockDb.delete.mockReturnValue({ where: deleteWhereMock });

            const result = await validateSessionToken('expired-token');
            
            expect(result).toEqual({ session: null, user: null });
            expect(mockDb.delete).toHaveBeenCalledWith(table.session);
            expect(deleteWhereMock).toHaveBeenCalled();
        });
	});

	describe('invalidateSession', () => {
		it('should delete the session', async () => {
			const whereMock = vi.fn().mockResolvedValue(undefined);
			mockDb.delete.mockReturnValue({ where: whereMock });

			await invalidateSession('session-id');

			expect(mockDb.delete).toHaveBeenCalledWith(table.session);
			expect(whereMock).toHaveBeenCalled();
		});
	});
});
