import { describe, expect, it, vi, beforeEach } from 'vitest';
import { AgentRegistry, RegistryError } from '../src/lib/server/rat/store.js';

// Mock DB
const mocks = vi.hoisted(() => ({
    mockGet: vi.fn(),
    mockRun: vi.fn(),
    mockAll: vi.fn(() => []),
    mockTransaction: vi.fn((cb) => cb({
        select: () => ({ from: () => ({ where: () => ({ get: mocks.mockGet, all: mocks.mockAll }), orderBy: () => ({ all: mocks.mockAll }), all: mocks.mockAll }) }),
        update: () => ({ set: () => ({ where: () => ({ run: mocks.mockRun }) }) }),
        delete: () => ({ where: () => ({ run: mocks.mockRun }), run: mocks.mockRun }),
        insert: () => ({ values: () => ({ run: mocks.mockRun }) })
    }))
}));

vi.mock('$lib/server/db', () => ({
    db: {
        select: vi.fn(() => ({
            from: vi.fn(() => ({
                where: vi.fn(() => ({ get: mocks.mockGet, all: mocks.mockAll })),
                orderBy: vi.fn(() => ({ all: mocks.mockAll })),
                all: mocks.mockAll
            })),
            all: mocks.mockAll
        })),
        transaction: mocks.mockTransaction
    }
}));

vi.mock('$lib/server/db/schema', () => ({
    agent: { id: 'id' },
    agentNote: { agentId: 'agentId' },
    agentCommand: { agentId: 'agentId' },
    agentResult: { agentId: 'agentId' },
    auditEvent: { commandId: 'commandId' },
    registrySubscription: { adminId: 'adminId' },
    enrollmentToken: { token: 'token' }
}));

vi.mock('../src/lib/server/logger', () => ({
    logger: {
        error: vi.fn(),
        info: vi.fn(),
        warn: vi.fn(),
        debug: vi.fn()
    }
}));

// Mock PluginTelemetryStore
vi.mock('../src/lib/server/plugins/telemetry-store.js', () => {
    return {
        PluginTelemetryStore: class {
            syncAgent = vi.fn();
            getAgentManifestDelta = vi.fn();
        }
    };
});

describe('AgentRegistry token validation', () => {
    let registry: AgentRegistry;

    beforeEach(() => {
        vi.clearAllMocks();
        process.env.TENVY_SHARED_SECRET = 'global-secret';
        // We need to prevent the constructor from loading from DB or at least mock it
        registry = new AgentRegistry();
    });

    it('validates using global shared secret', () => {
        // @ts-ignore - accessing private for testing
        expect(() => registry['validateToken']('global-secret')).not.toThrow();
    });

    it('throws if token is missing', () => {
        // @ts-ignore
        expect(() => registry['validateToken'](undefined)).toThrow(RegistryError);
    });

    it('validates using database enrollment token', () => {
        mocks.mockGet.mockReturnValue({
            token: 'db-token',
            uses: 0,
            maxUses: 1,
            expiresAt: null,
            revokedAt: null
        });

        // @ts-ignore
        expect(() => registry['validateToken']('db-token')).not.toThrow();
        expect(mocks.mockRun).toHaveBeenCalled();
    });

    it('rejects revoked token', () => {
        mocks.mockGet.mockReturnValue({
            token: 'revoked-token',
            uses: 0,
            maxUses: 1,
            expiresAt: null,
            revokedAt: new Date()
        });

        // @ts-ignore
        expect(() => registry['validateToken']('revoked-token')).toThrow('Invalid registration token');
    });

    it('rejects expired token', () => {
        mocks.mockGet.mockReturnValue({
            token: 'expired-token',
            uses: 0,
            maxUses: 1,
            expiresAt: new Date(Date.now() - 1000),
            revokedAt: null
        });

        // @ts-ignore
        expect(() => registry['validateToken']('expired-token')).toThrow('Invalid registration token');
    });

    it('rejects exhausted token', () => {
        mocks.mockGet.mockReturnValue({
            token: 'exhausted-token',
            uses: 1,
            maxUses: 1,
            expiresAt: null,
            revokedAt: null
        });

        // @ts-ignore
        expect(() => registry['validateToken']('exhausted-token')).toThrow('Invalid registration token');
    });
});
