import {
	sqliteTable,
	integer,
	text,
	uniqueIndex,
	primaryKey,
	index
} from 'drizzle-orm/sqlite-core';

const timestamp = (
	name: string,
	{ optional = false, defaultNow = false }: { optional?: boolean; defaultNow?: boolean } = {}
) => {
	let column = integer(name, { mode: 'timestamp' });
	if (!optional) {
		column = column.notNull();
	}
	if (defaultNow) {
		column = column.$defaultFn(() => new Date());
	}
	return column;
};

export const voucher = sqliteTable(
	'voucher',
	{
		id: text('id').primaryKey(),
		codeHash: text('code_hash').notNull(),
		createdAt: timestamp('created_at', { defaultNow: true }),
		expiresAt: timestamp('expires_at', { optional: true }),
		revokedAt: timestamp('revoked_at', { optional: true }),
		redeemedAt: timestamp('redeemed_at', { optional: true })
	},
	(table) => ({
		codeHashIdx: uniqueIndex('voucher_code_hash_idx').on(table.codeHash)
	})
);

export const user = sqliteTable('user', {
	id: text('id').primaryKey(),
	createdAt: timestamp('created_at', { defaultNow: true }),
	voucherId: text('voucher_id')
		.notNull()
		.references(() => voucher.id),
	role: text('role').notNull().default('operator'),
	passkeyRegistered: integer('passkey_registered', { mode: 'boolean' }).notNull().default(false),
	currentChallenge: text('current_challenge'),
	challengeType: text('challenge_type'),
	challengeExpiresAt: timestamp('challenge_expires_at', { optional: true })
});

export const session = sqliteTable('session', {
	id: text('id').primaryKey(),
	userId: text('user_id')
		.notNull()
		.references(() => user.id),
	expiresAt: timestamp('expires_at'),
	createdAt: timestamp('created_at', { defaultNow: true }),
	description: text('description')
});

export const passkey = sqliteTable('passkey', {
	id: text('id').primaryKey(),
	userId: text('user_id')
		.notNull()
		.references(() => user.id),
	publicKey: text('public_key').notNull(),
	counter: integer('counter').notNull().default(0),
	deviceType: text('device_type'),
	backedUp: integer('backed_up', { mode: 'boolean' }).notNull().default(false),
	transports: text('transports'),
	createdAt: timestamp('created_at', { defaultNow: true }),
	lastUsedAt: timestamp('last_used_at', { optional: true })
});

export const recoveryCode = sqliteTable('recovery_code', {
	id: integer('id').primaryKey({ autoIncrement: true }),
	userId: text('user_id')
		.notNull()
		.references(() => user.id),
	codeHash: text('code_hash').notNull(),
	createdAt: timestamp('created_at', { defaultNow: true }),
	consumedAt: timestamp('consumed_at', { optional: true })
});

export const plugin = sqliteTable('plugin', {
	id: text('id').primaryKey(),
	status: text('status').notNull().default('active'),
	enabled: integer('enabled', { mode: 'boolean' }).notNull().default(true),
	autoUpdate: integer('auto_update', { mode: 'boolean' }).notNull().default(false),
	runtimeType: text('runtime_type').notNull().default('native'),
	sandboxed: integer('sandboxed', { mode: 'boolean' }).notNull().default(false),
	installations: integer('installations').notNull().default(0),
	manualTargets: integer('manual_targets').notNull().default(0),
	autoTargets: integer('auto_targets').notNull().default(0),
	defaultDeliveryMode: text('default_delivery_mode').notNull().default('manual'),
	allowManualPush: integer('allow_manual_push', { mode: 'boolean' }).notNull().default(true),
	allowAutoSync: integer('allow_auto_sync', { mode: 'boolean' }).notNull().default(false),
	lastManualPushAt: timestamp('last_manual_push_at', { optional: true }),
	lastAutoSyncAt: timestamp('last_auto_sync_at', { optional: true }),
	lastDeployedAt: timestamp('last_deployed_at', { optional: true }),
	lastCheckedAt: timestamp('last_checked_at', { optional: true }),
	signatureStatus: text('signature_status').notNull().default('unsigned'),
	signatureTrusted: integer('signature_trusted', { mode: 'boolean' }).notNull().default(false),
	signatureType: text('signature_type').notNull().default('none'),
	signatureHash: text('signature_hash'),
	signatureSigner: text('signature_signer'),
	signaturePublicKey: text('signature_public_key'),
	signatureCheckedAt: timestamp('signature_checked_at', { optional: true }),
	signatureSignedAt: timestamp('signature_signed_at', { optional: true }),
	signatureError: text('signature_error'),
	signatureErrorCode: text('signature_error_code'),
	signatureChain: text('signature_chain'),
	approvalStatus: text('approval_status').notNull().default('pending'),
	approvedAt: timestamp('approved_at', { optional: true }),
	approvalNote: text('approval_note'),
	createdAt: timestamp('created_at', { defaultNow: true }),
	updatedAt: timestamp('updated_at', { defaultNow: true })
});

export const pluginRegistryEntry = sqliteTable(
	'plugin_registry_entry',
	{
		id: text('id').primaryKey(),
		pluginId: text('plugin_id').notNull(),
		version: text('version').notNull(),
		manifest: text('manifest').notNull(),
		manifestDigest: text('manifest_digest').notNull(),
		artifactHash: text('artifact_hash'),
		artifactSizeBytes: integer('artifact_size_bytes'),
		metadata: text('metadata'),
		approvalStatus: text('approval_status').notNull().default('pending'),
		publishedBy: text('published_by').references(() => user.id, { onDelete: 'set null' }),
		publishedAt: timestamp('published_at', { defaultNow: true }),
		approvedBy: text('approved_by').references(() => user.id, { onDelete: 'set null' }),
		approvedAt: timestamp('approved_at', { optional: true }),
		approvalNote: text('approval_note'),
		revokedBy: text('revoked_by').references(() => user.id, { onDelete: 'set null' }),
		revokedAt: timestamp('revoked_at', { optional: true }),
		revocationReason: text('revocation_reason'),
		createdAt: timestamp('created_at', { defaultNow: true }),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		pluginVersionIdx: uniqueIndex('plugin_registry_entry_plugin_version_idx').on(
			table.pluginId,
			table.version
		),
		statusIdx: index('plugin_registry_entry_status_idx').on(table.approvalStatus)
	})
);

export const pluginInstallation = sqliteTable(
	'plugin_installation',
	{
		pluginId: text('plugin_id')
			.notNull()
			.references(() => plugin.id, { onDelete: 'cascade' }),
		agentId: text('agent_id')
			.notNull()
			.references(() => agent.id, { onDelete: 'cascade' }),
		status: text('status').notNull().default('pending'),
		version: text('version').notNull(),
		hash: text('hash'),
		enabled: integer('enabled', { mode: 'boolean' }).notNull().default(true),
		error: text('error'),
		lastDeployedAt: timestamp('last_deployed_at', { optional: true }),
		lastCheckedAt: timestamp('last_checked_at', { optional: true }),
		createdAt: timestamp('created_at', { defaultNow: true }),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		pk: primaryKey({ columns: [table.pluginId, table.agentId] }),
		agentIdx: index('plugin_installation_agent_idx').on(table.agentId)
	})
);

export const pluginMarketplaceListing = sqliteTable(
	'plugin_marketplace_listing',
	{
		id: text('id').primaryKey(),
		pluginId: text('plugin_id').notNull(),
		name: text('name').notNull(),
		summary: text('summary'),
		repositoryUrl: text('repository_url').notNull(),
		version: text('version').notNull(),
		manifest: text('manifest').notNull(),
		pricingTier: text('pricing_tier').notNull().default('free'),
		status: text('status').notNull().default('pending'),
		submittedBy: text('submitted_by').references(() => user.id, { onDelete: 'set null' }),
		reviewerId: text('reviewer_id').references(() => user.id, { onDelete: 'set null' }),
		licenseSpdxId: text('license_spdx_id').notNull(),
		licenseName: text('license_name'),
		licenseUrl: text('license_url'),
		signatureType: text('signature_type').notNull(),
		signatureHash: text('signature_hash').notNull(),
		signaturePublicKey: text('signature_public_key'),
		signature: text('signature').notNull(),
		signedAt: timestamp('signed_at', { optional: true }),
		signatureStatus: text('signature_status').notNull().default('unsigned'),
		signatureTrusted: integer('signature_trusted', { mode: 'boolean' }).notNull().default(false),
		signatureSigner: text('signature_signer'),
		signatureCheckedAt: timestamp('signature_checked_at', { optional: true }),
		signatureError: text('signature_error'),
		signatureErrorCode: text('signature_error_code'),
		signatureChain: text('signature_chain'),
		submittedAt: timestamp('submitted_at', { defaultNow: true }),
		reviewedAt: timestamp('reviewed_at', { optional: true }),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		pluginIdx: uniqueIndex('plugin_marketplace_listing_plugin_idx').on(table.pluginId)
	})
);

export const pluginMarketplaceEntitlement = sqliteTable(
	'plugin_marketplace_entitlement',
	{
		id: text('id').primaryKey(),
		listingId: text('listing_id')
			.notNull()
			.references(() => pluginMarketplaceListing.id, { onDelete: 'cascade' }),
		tenantId: text('tenant_id')
			.notNull()
			.references(() => voucher.id, { onDelete: 'cascade' }),
		seats: integer('seats').notNull().default(1),
		status: text('status').notNull().default('active'),
		grantedBy: text('granted_by').references(() => user.id, { onDelete: 'set null' }),
		grantedAt: timestamp('granted_at', { defaultNow: true }),
		expiresAt: timestamp('expires_at', { optional: true }),
		metadata: text('metadata'),
		lastSyncedAt: timestamp('last_synced_at', { optional: true })
	},
	(table) => ({
		tenantListingIdx: uniqueIndex('plugin_entitlement_tenant_listing_idx').on(
			table.tenantId,
			table.listingId
		)
	})
);

export const pluginMarketplaceTransaction = sqliteTable(
	'plugin_marketplace_transaction',
	{
		id: text('id').primaryKey(),
		listingId: text('listing_id')
			.notNull()
			.references(() => pluginMarketplaceListing.id, { onDelete: 'cascade' }),
		tenantId: text('tenant_id')
			.notNull()
			.references(() => voucher.id, { onDelete: 'cascade' }),
		entitlementId: text('entitlement_id').references(() => pluginMarketplaceEntitlement.id, {
			onDelete: 'set null'
		}),
		amount: integer('amount').notNull().default(0),
		currency: text('currency').notNull().default('credits'),
		status: text('status').notNull().default('pending'),
		createdAt: timestamp('created_at', { defaultNow: true }),
		processedAt: timestamp('processed_at', { optional: true }),
		metadata: text('metadata')
	},
	(table) => ({
		entitlementIdx: index('plugin_marketplace_transaction_entitlement_idx').on(table.entitlementId)
	})
);

export const registrySubscription = sqliteTable(
	'registry_subscription',
	{
		id: text('id').primaryKey(),
		adminId: text('admin_id').notNull(),
		channel: text('channel').notNull(),
		cursor: integer('cursor').notNull().default(0),
		snapshot: text('snapshot').notNull(),
		createdAt: timestamp('created_at', { defaultNow: true }),
		lastSeenAt: timestamp('last_seen_at', { defaultNow: true }),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		adminChannelIdx: uniqueIndex('registry_subscription_admin_channel_idx').on(
			table.adminId,
			table.channel
		)
	})
);

export const agent = sqliteTable(
	'agent',
	{
		id: text('id').primaryKey(),
		keyHash: text('key_hash').notNull(),
		metadata: text('metadata').notNull(),
		status: text('status').notNull().default('offline'),
		connectedAt: timestamp('connected_at', { defaultNow: true }),
		lastSeen: timestamp('last_seen', { defaultNow: true }),
		metrics: text('metrics'),
		config: text('config').notNull(),
		optionsState: text('options_state'),
		downloadsCatalogue: text('downloads_catalogue'),
		operatorNote: text('operator_note'),
		operatorNoteTags: text('operator_note_tags'),
		operatorNoteUpdatedAt: timestamp('operator_note_updated_at', { optional: true }),
		operatorNoteUpdatedBy: text('operator_note_updated_by'),
		fingerprint: text('fingerprint').notNull(),
		createdAt: timestamp('created_at', { defaultNow: true }),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		fingerprintIdx: uniqueIndex('agent_fingerprint_idx').on(table.fingerprint)
	})
);

export const agentNote = sqliteTable(
	'agent_note',
	{
		agentId: text('agent_id').notNull(),
		noteId: text('note_id').notNull(),
		ciphertext: text('ciphertext').notNull(),
		nonce: text('nonce').notNull(),
		digest: text('digest').notNull(),
		version: integer('version').notNull().default(1),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		pk: primaryKey({ columns: [table.agentId, table.noteId] })
	})
);

export const agentCommand = sqliteTable('agent_command', {
	id: text('id').primaryKey(),
	agentId: text('agent_id')
		.notNull()
		.references(() => agent.id, { onDelete: 'cascade' }),
	name: text('name').notNull(),
	payload: text('payload').notNull(),
	createdAt: timestamp('created_at', { defaultNow: true })
});

export const auditEvent = sqliteTable(
	'audit_event',
	{
		id: integer('id').primaryKey({ autoIncrement: true }),
		commandId: text('command_id').notNull(),
		agentId: text('agent_id')
			.notNull()
			.references(() => agent.id, { onDelete: 'cascade' }),
		operatorId: text('operator_id').references(() => user.id, { onDelete: 'set null' }),
		commandName: text('command_name').notNull(),
		payloadHash: text('payload_hash').notNull(),
		queuedAt: timestamp('queued_at', { defaultNow: true }),
		acknowledgedAt: timestamp('acknowledged_at', { optional: true }),
		acknowledgement: text('acknowledgement'),
		executedAt: timestamp('executed_at', { optional: true }),
		result: text('result')
	},
	(table) => ({
		commandUnique: uniqueIndex('audit_event_command_idx').on(table.commandId),
		agentIdx: index('audit_event_agent_idx').on(table.agentId)
	})
);

export const agentResult = sqliteTable(
	'agent_result',
	{
		id: integer('id').primaryKey({ autoIncrement: true }),
		agentId: text('agent_id')
			.notNull()
			.references(() => agent.id, { onDelete: 'cascade' }),
		commandId: text('command_id').notNull(),
		success: integer('success', { mode: 'boolean' }).notNull().default(true),
		output: text('output'),
		error: text('error'),
		completedAt: timestamp('completed_at', { defaultNow: true })
	},
	(table) => ({
		uniqueCommand: uniqueIndex('agent_result_command_idx').on(table.agentId, table.commandId)
	})
);

export const keyloggerSession = sqliteTable(
	'keylogger_session',
	{
		id: text('id').primaryKey(),
		agentId: text('agent_id')
			.notNull()
			.references(() => agent.id, { onDelete: 'cascade' }),
		mode: text('mode').notNull(),
		startedAt: timestamp('started_at', { defaultNow: true }),
		active: integer('active', { mode: 'boolean' }).notNull().default(true),
		config: text('config').notNull(),
		totalEvents: integer('total_events').notNull().default(0),
		lastCapturedAt: timestamp('last_captured_at', { optional: true }),
		createdAt: timestamp('created_at', { defaultNow: true }),
		updatedAt: timestamp('updated_at', { defaultNow: true })
	},
	(table) => ({
		agentIdx: index('keylogger_session_agent_idx').on(table.agentId)
	})
);

export const keyloggerBatch = sqliteTable(
	'keylogger_batch',
	{
		id: text('id').primaryKey(),
		sessionId: text('session_id')
			.notNull()
			.references(() => keyloggerSession.id, { onDelete: 'cascade' }),
		agentId: text('agent_id')
			.notNull()
			.references(() => agent.id, { onDelete: 'cascade' }),
		capturedAt: timestamp('captured_at').notNull(),
		events: text('events').notNull(),
		totalEvents: integer('total_events').notNull(),
		createdAt: timestamp('created_at', { defaultNow: true })
	},
	(table) => ({
		sessionIdx: index('keylogger_batch_session_idx').on(table.sessionId),
		agentIdx: index('keylogger_batch_agent_idx').on(table.agentId)
	})
);

export type Session = typeof session.$inferSelect;

export type User = typeof user.$inferSelect;

export type Voucher = typeof voucher.$inferSelect;

export type Passkey = typeof passkey.$inferSelect;

export type RecoveryCode = typeof recoveryCode.$inferSelect;
export type Plugin = typeof plugin.$inferSelect;
export type PluginInstallation = typeof pluginInstallation.$inferSelect;
export type Agent = typeof agent.$inferSelect;
export type AgentNote = typeof agentNote.$inferSelect;
export type AgentCommand = typeof agentCommand.$inferSelect;
export type AgentResult = typeof agentResult.$inferSelect;
export type AuditEvent = typeof auditEvent.$inferSelect;
export type KeyloggerSession = typeof keyloggerSession.$inferSelect;
export type KeyloggerBatch = typeof keyloggerBatch.$inferSelect;
