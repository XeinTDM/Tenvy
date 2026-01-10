import type { Cookies, ActionFailure } from '@sveltejs/kit';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { db } from '../src/lib/server/db/index.js';
import { hashVoucherCode, sessionCookieName } from '../src/lib/server/auth.js';
import {
	voucher as voucherTable,
	user as userTable,
	session as sessionTable
} from '../src/lib/server/db/schema.js';
import { eq } from 'drizzle-orm';

vi.mock('$env/dynamic/private', () => import('./mocks/env-dynamic-private'));

const redeemModulePromise = import('../src/routes/redeem/+page.server.js');
type RedeemModule = Awaited<typeof redeemModulePromise>;
type RedeemAction = NonNullable<RedeemModule['actions']>['default'];
type RedeemEvent = Parameters<RedeemAction>[0];
type RedeemEventWithTestCookies = RedeemEvent & { cookies: TestCookies };

interface TestCookies extends Cookies {
	store: Map<string, string>;
}

function createCookieJar(): TestCookies {
	const store = new Map<string, string>();
	return {
		get(name: string) {
			return store.get(name);
		},
		getAll() {
			return Array.from(store.entries()).map(([name, value]) => ({ name, value }));
		},
		set(name: string, value: string) {
			store.set(name, value);
		},
		delete(name: string) {
			store.delete(name);
		},
		serialize(name: string, value: string) {
			return `${name}=${value}`;
		},
		store
	};
}

function createRedeemEvent(
	voucherCode: string,
	clientAddress = '127.0.0.1'
): RedeemEventWithTestCookies {
	const url = new URL('https://controller.test/redeem');
	const formData = new FormData();
	formData.set('voucher', voucherCode);

	const tracing: RedeemEvent['tracing'] = {
		enabled: false,
		root: {} as RedeemEvent['tracing']['root'],
		current: {} as RedeemEvent['tracing']['current']
	};

	return {
		params: {},
		route: { id: '/redeem' },
		url,
		request: new Request(url, { method: 'POST', body: formData }),
		locals: { user: null, session: null },
		cookies: createCookieJar(),
		fetch,
		setHeaders: () => {},
		platform: {},
		isDataRequest: false,
		isSubRequest: false,
		tracing,
		isRemoteRequest: false,
		getClientAddress: () => clientAddress
	} as RedeemEventWithTestCookies;
}

async function clearTables() {
	await Promise.all([
		db.delete(sessionTable).run(),
		db.delete(userTable).run(),
		db.delete(voucherTable).run()
	]);
}

async function loadRedeemAction() {
	const module = await redeemModulePromise;
	const action = module.actions?.default;
	if (!action) {
		throw new Error('redeem action missing');
	}
	return action;
}

describe('redeem page actions', () => {
	beforeEach(async () => {
		await clearTables();
	});

	afterEach(async () => {
		await clearTables();
	});

	it('redeems a voucher and provisions a session', async () => {
		const action = await loadRedeemAction();

		const voucherCode = 'TEN-REDEEM-SUCCESS-0001';
		const voucherId = 'voucher-success';
		await db
			.insert(voucherTable)
			.values({
				id: voucherId,
				codeHash: hashVoucherCode(voucherCode),
				createdAt: new Date()
			})
			.run();

		const event = createRedeemEvent(voucherCode, '10.0.0.1');
		const result = await action(event);

		expect(result).toEqual({ success: true });
		expect(event.locals.user).toMatchObject({
			role: 'operator',
			passkeyRegistered: false,
			voucherId,
			voucherActive: true,
			voucherExpiresAt: null
		});
		expect(typeof event.locals.user?.id).toBe('string');
		expect(event.cookies.store.get(sessionCookieName)).toBeTruthy();

		const storedVoucher = db
			.select()
			.from(voucherTable)
			.where(eq(voucherTable.id, voucherId))
			.get();
		expect(storedVoucher?.redeemedAt).toBeInstanceOf(Date);

		const storedUser = db
			.select()
			.from(userTable)
			.where(eq(userTable.id, event.locals.user!.id))
			.get();
		expect(storedUser?.voucherId).toBe(voucherId);

		const storedSession = db
			.select()
			.from(sessionTable)
			.where(eq(sessionTable.userId, event.locals.user!.id))
			.get();
		expect(storedSession).toBeDefined();
		expect(storedSession?.description).toBe('voucher-onboarding');
	});

	it('rejects an expired voucher', async () => {
		const action = await loadRedeemAction();

		const voucherCode = 'TEN-REDEEM-EXPIRED-0001';
		const voucherId = 'voucher-expired';
		db.insert(voucherTable)
			.values({
				id: voucherId,
				codeHash: hashVoucherCode(voucherCode),
				createdAt: new Date(),
				expiresAt: new Date(Date.now() - 1_000)
			})
			.run();

		const event = createRedeemEvent(voucherCode, '10.0.0.2');
		const response = (await action(event)) as ActionFailure<{
			message: string;
			values: { voucher: string };
		}>;
		expect(response.status).toBe(400);
		expect(response.data?.message).toMatch(/expired/i);
		expect(response.data?.values?.voucher).toBe(voucherCode);

		const storedVoucher = db
			.select()
			.from(voucherTable)
			.where(eq(voucherTable.id, voucherId))
			.get();
		expect(storedVoucher?.redeemedAt).toBeNull();
	});

	it('rejects a revoked voucher', async () => {
		const action = await loadRedeemAction();

		const voucherCode = 'TEN-REDEEM-REVOKED-0001';
		const voucherId = 'voucher-revoked';
		await db
			.insert(voucherTable)
			.values({
				id: voucherId,
				codeHash: hashVoucherCode(voucherCode),
				createdAt: new Date(),
				revokedAt: new Date()
			})
			.run();

		const event = createRedeemEvent(voucherCode, '10.0.0.3');
		const response = (await action(event)) as ActionFailure<{
			message: string;
			values: { voucher: string };
		}>;
		expect(response.status).toBe(400);
		expect(response.data?.message).toMatch(/revoked/i);
		expect(response.data?.values?.voucher).toBe(voucherCode);
	});

	it('enforces rate limiting per client address', async () => {
		const action = await loadRedeemAction();
		const address = '10.0.0.4';
		const voucherCode = 'TEN-REDEEM-RATELIMIT-0001';

		for (let attempt = 0; attempt < 6; attempt++) {
			const event = createRedeemEvent(voucherCode, address);
			const response = (await action(event)) as ActionFailure<{
				message: string;
				values: { voucher: string };
			}>;

			if (attempt < 5) {
				expect(response.status).toBe(400);
			} else {
				expect(response.status).toBe(429);
				expect(response.data?.message).toMatch(/too many attempts/i);
			}
		}
	});
});
