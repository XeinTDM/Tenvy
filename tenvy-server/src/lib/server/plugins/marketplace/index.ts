import { eq } from 'drizzle-orm';
import { randomUUID } from 'node:crypto';
import { db } from '$lib/server/db/index.js';
import {
	pluginMarketplaceListing,
	pluginMarketplaceEntitlement,
	pluginMarketplaceTransaction
} from '$lib/server/db/schema.js';
import type {
	PluginManifest,
	PluginLicenseInfo,
	PluginSignatureVerificationError,
	PluginSignatureVerificationResult,
	PluginSignatureVerificationSummary,
	PluginSignatureStatus,
	PluginSignatureType
} from '../../../../../../shared/types/plugin-manifest';
import {
	validatePluginManifest,
	verifyPluginSignature,
	resolveManifestSignature,
	isPluginSignatureType
} from '../../../../../../shared/types/plugin-manifest';
import { getVerificationOptions } from '$lib/server/plugins/signature-policy.js';

type MarketplaceListingRow = typeof pluginMarketplaceListing.$inferSelect;
type MarketplaceEntitlementRow = typeof pluginMarketplaceEntitlement.$inferSelect;
type MarketplaceTransactionRow = typeof pluginMarketplaceTransaction.$inferSelect;

export type MarketplaceSignatureState = {
	status: PluginSignatureStatus;
	trusted: boolean;
	type: PluginSignatureType;
	hash?: string | null;
	signer?: string | null;
	publicKey?: string | null;
	signedAt?: Date | null;
	checkedAt?: Date | null;
	error?: string | null;
	errorCode?: string | null;
	certificateChain?: string[] | null;
};

export type MarketplaceListing = Omit<MarketplaceListingRow, 'signature'> & {
	manifestObject: PluginManifest;
	signature: MarketplaceSignatureState;
};
export type MarketplaceEntitlement = MarketplaceEntitlementRow & {
	listing: MarketplaceListing;
	transaction?: MarketplaceTransactionRow | null;
};

export type MarketplaceStatus = 'pending' | 'approved' | 'rejected';

export interface SubmitListingInput {
	manifest: PluginManifest;
	summary?: string;
	pricingTier?: string;
	submittedBy?: string | null;
}

export interface ReviewListingInput {
	id: string;
	reviewerId: string;
	status: Exclude<MarketplaceStatus, 'pending'>;
	note?: string | null;
}

export interface CreateEntitlementInput {
	listingId: string;
	tenantId: string;
	seats?: number;
	grantedBy?: string | null;
	expiresAt?: Date | null;
	metadata?: Record<string, unknown> | null;
	amountCents?: number;
	currency?: string;
}

export class MarketplaceError extends Error {
	details: string[];

	constructor(message: string, details: string[] = []) {
		super(message);
		this.name = 'MarketplaceError';
		this.details = details;
	}
}

const parseManifest = (raw: string): PluginManifest => {
	const parsed = JSON.parse(raw) as PluginManifest;
	const errors = validatePluginManifest(parsed);
	if (errors.length > 0) {
		throw new MarketplaceError('Persisted manifest failed validation', errors);
	}
	return parsed;
};

const now = () => new Date();

const licenseDetails = (license: PluginLicenseInfo | undefined) => ({
	licenseSpdxId: license?.spdxId ?? 'UNKNOWN',
	licenseName: license?.name ?? null,
	licenseUrl: license?.url ?? null
});

const normalizeHash = (value: string | undefined | null): string =>
	value?.trim().toLowerCase() ?? '';

const baseSignatureSummary = (manifest: PluginManifest): PluginSignatureVerificationSummary => {
	const metadata = resolveManifestSignature(manifest);
	const chain = metadata.certificateChain?.length ? [...metadata.certificateChain] : undefined;
	const resolvedType = isPluginSignatureType(metadata.type) ? metadata.type : 'sha256';
	const normalizedHash = normalizeHash(metadata.hash ?? manifest.package?.hash);

	return {
		trusted: false,
		signatureType: resolvedType,
		hash: normalizedHash,
		signer: metadata.signer ?? null,
		signedAt: metadata.timestamp ? new Date(metadata.timestamp) : null,
		publicKey: null,
		certificateChain: chain,
		checkedAt: new Date(),
		status: !metadata.type || metadata.type === 'none' ? 'unsigned' : 'untrusted',
		error: undefined,
		errorCode: undefined
	};
};

const summarizeVerificationSuccess = (
	manifest: PluginManifest,
	result: PluginSignatureVerificationResult
): PluginSignatureVerificationSummary => {
	const summary = baseSignatureSummary(manifest);
	summary.checkedAt = new Date();
	summary.trusted = result.trusted;
	summary.signatureType = result.signatureType;
	summary.hash = result.hash ?? summary.hash;
	summary.signer = result.signer ?? summary.signer ?? null;
	summary.publicKey = result.publicKey ?? summary.publicKey ?? null;
	summary.certificateChain = result.certificateChain?.length
		? [...result.certificateChain]
		: summary.certificateChain;
	summary.signedAt = result.signedAt ?? summary.signedAt;

	const declaredType = resolveManifestSignature(manifest).type?.trim().toLowerCase();

	if (result.trusted) {
		summary.status = 'trusted';
	} else if (!declaredType || declaredType === 'none') {
		summary.status = 'unsigned';
	} else {
		summary.status = 'untrusted';
	}

	return summary;
};

const summarizeVerificationFailure = (
	manifest: PluginManifest,
	error: PluginSignatureVerificationError | Error
): PluginSignatureVerificationSummary => {
	const summary = baseSignatureSummary(manifest);
	summary.checkedAt = new Date();
	summary.trusted = false;
	summary.error = error.message;
	if ('code' in error && typeof error.code === 'string') {
		summary.errorCode = error.code;
		summary.status = error.code === 'UNSIGNED' ? 'unsigned' : 'invalid';
	} else {
		summary.status = 'invalid';
	}
	return summary;
};

const resolveSignatureSummary = async (
	manifest: PluginManifest
): Promise<PluginSignatureVerificationSummary> => {
	const options = getVerificationOptions();
	try {
		const result = await verifyPluginSignature(manifest, options);
		return summarizeVerificationSuccess(manifest, result);
	} catch (error) {
		return summarizeVerificationFailure(
			manifest,
			error as PluginSignatureVerificationError | Error
		);
	}
};

const signatureDetails = async (manifest: PluginManifest) => {
	const summary = await resolveSignatureSummary(manifest);
	const chain = summary.certificateChain?.length ? JSON.stringify(summary.certificateChain) : null;
	const metadata = resolveManifestSignature(manifest);
	const signature = metadata.value ?? '';
	return {
		signatureType: summary.signatureType,
		signatureHash: summary.hash ?? '',
		signaturePublicKey: summary.publicKey ?? null,
		signature,
		signedAt: summary.signedAt ?? null,
		signatureStatus: summary.status,
		signatureTrusted: summary.trusted,
		signatureSigner: summary.signer ?? null,
		signatureCheckedAt: summary.checkedAt,
		signatureError: summary.error ?? null,
		signatureErrorCode: summary.errorCode ?? null,
		signatureChain: chain
	};
};

const parseSignatureChain = (raw: string | null | undefined): string[] | null => {
	if (!raw) return null;
	try {
		const parsed = JSON.parse(raw);
		return Array.isArray(parsed)
			? parsed.filter((value): value is string => typeof value === 'string')
			: null;
	} catch {
		return null;
	}
};

const assembleSignatureState = (row: MarketplaceListingRow): MarketplaceSignatureState => ({
	status: row.signatureStatus as PluginSignatureStatus,
	trusted: Boolean(row.signatureTrusted),
	type: row.signatureType as PluginSignatureType,
	hash: row.signatureHash ?? null,
	signer: row.signatureSigner ?? null,
	publicKey: row.signaturePublicKey ?? null,
	signedAt: row.signedAt ?? null,
	checkedAt: row.signatureCheckedAt ?? null,
	error: row.signatureError ?? null,
	errorCode: row.signatureErrorCode ?? null,
	certificateChain: parseSignatureChain(row.signatureChain)
});

const assembleListing = (row: MarketplaceListingRow): MarketplaceListing => {
	return {
		...row,
		manifestObject: parseManifest(row.manifest),
		signature: assembleSignatureState(row)
	} as MarketplaceListing;
};

export async function submitListing(input: SubmitListingInput): Promise<MarketplaceListing> {
	const { manifest } = input;
	const problems = validatePluginManifest(manifest);
	const trimmedRepositoryUrl = manifest.repositoryUrl?.trim();
	if (!trimmedRepositoryUrl) {
		problems.push('repositoryUrl is required');
	}
	if (problems.length > 0) {
		throw new MarketplaceError('Invalid plugin manifest', problems);
	}
	const repositoryUrl = trimmedRepositoryUrl!;

	const summary = input.summary?.trim().length
		? input.summary.trim()
		: (manifest.description ?? '');
	const pricingTier = input.pricingTier?.trim() ?? 'free';
	const signature = await signatureDetails(manifest);

	const existing = await db
		.select()
		.from(pluginMarketplaceListing)
		.where(eq(pluginMarketplaceListing.pluginId, manifest.id))
		.limit(1);

	const submitter = input.submittedBy ?? existing[0]?.submittedBy ?? null;

	const baseRecord = {
		pluginId: manifest.id,
		name: manifest.name,
		summary,
		repositoryUrl,
		version: manifest.version,
		manifest: JSON.stringify(manifest),
		pricingTier,
		status: 'pending' as MarketplaceStatus,
		submittedBy: submitter,
		reviewedAt: null,
		reviewerId: null,
		updatedAt: now(),
		...licenseDetails(manifest.license),
		...signature
	} satisfies Partial<MarketplaceListingRow>;

	if (existing.length > 0) {
		await db
			.update(pluginMarketplaceListing)
			.set(baseRecord)
			.where(eq(pluginMarketplaceListing.id, existing[0].id));

		const [updated] = await db
			.select()
			.from(pluginMarketplaceListing)
			.where(eq(pluginMarketplaceListing.id, existing[0].id))
			.limit(1);

		if (!updated) throw new MarketplaceError('Failed to refresh marketplace listing');
		return assembleListing(updated);
	}

	const id = randomUUID();
	await db.insert(pluginMarketplaceListing).values({
		id,
		submittedAt: now(),
		...baseRecord
	});

	const [created] = await db
		.select()
		.from(pluginMarketplaceListing)
		.where(eq(pluginMarketplaceListing.id, id))
		.limit(1);

	if (!created) throw new MarketplaceError('Failed to persist marketplace listing');
	return assembleListing(created);
}

export async function listListings(status?: MarketplaceStatus): Promise<MarketplaceListing[]> {
	const rows = status
		? await db
				.select()
				.from(pluginMarketplaceListing)
				.where(eq(pluginMarketplaceListing.status, status))
				.orderBy(pluginMarketplaceListing.name)
		: await db.select().from(pluginMarketplaceListing).orderBy(pluginMarketplaceListing.name);
	return rows.map(assembleListing);
}

export async function getListing(id: string): Promise<MarketplaceListing | null> {
	const rows = await db
		.select()
		.from(pluginMarketplaceListing)
		.where(eq(pluginMarketplaceListing.id, id))
		.limit(1);
	const [row] = rows;
	return row ? assembleListing(row) : null;
}

export async function reviewListing(input: ReviewListingInput): Promise<MarketplaceListing> {
	const listing = await getListing(input.id);
	if (!listing) {
		throw new MarketplaceError('Marketplace listing not found');
	}

	if (input.status === 'approved') {
		if (listing.signature.status !== 'trusted' || !listing.signature.trusted) {
			throw new MarketplaceError(
				'Marketplace listings must have trusted signatures before approval'
			);
		}
	}

	await db
		.update(pluginMarketplaceListing)
		.set({
			status: input.status,
			reviewerId: input.reviewerId,
			reviewedAt: now(),
			updatedAt: now()
		})
		.where(eq(pluginMarketplaceListing.id, input.id));

	const updated = await getListing(input.id);
	if (!updated) {
		throw new MarketplaceError('Failed to update marketplace listing');
	}
	return updated;
}

export async function listEntitlementsForTenant(
	tenantId: string
): Promise<MarketplaceEntitlement[]> {
	if (!tenantId || tenantId.trim() === '') {
		return [];
	}

	const rows = await db
		.select({
			entitlement: pluginMarketplaceEntitlement,
			listing: pluginMarketplaceListing,
			transaction: pluginMarketplaceTransaction
		})
		.from(pluginMarketplaceEntitlement)
		.innerJoin(
			pluginMarketplaceListing,
			eq(pluginMarketplaceListing.id, pluginMarketplaceEntitlement.listingId)
		)
		.leftJoin(
			pluginMarketplaceTransaction,
			eq(pluginMarketplaceTransaction.entitlementId, pluginMarketplaceEntitlement.id)
		)
		.where(eq(pluginMarketplaceEntitlement.tenantId, tenantId));

	return rows.map(({ entitlement, listing, transaction }) => ({
		...entitlement,
		listing: assembleListing(listing),
		transaction: transaction ?? null
	}));
}

export async function createEntitlement(
	input: CreateEntitlementInput
): Promise<MarketplaceEntitlement> {
	const listing = await getListing(input.listingId);
	if (!listing) {
		throw new MarketplaceError('Marketplace listing not found');
	}
	if (listing.status !== 'approved') {
		throw new MarketplaceError('Listing must be approved before entitlements can be created');
	}

	const metadata = input.metadata ? JSON.stringify(input.metadata) : null;
	const entitlementId = randomUUID();

	await db.insert(pluginMarketplaceEntitlement).values({
		id: entitlementId,
		listingId: listing.id,
		tenantId: input.tenantId,
		seats: input.seats ?? 1,
		status: 'active',
		grantedBy: input.grantedBy ?? null,
		grantedAt: now(),
		expiresAt: input.expiresAt ?? null,
		metadata,
		lastSyncedAt: null
	});

	await db.insert(pluginMarketplaceTransaction).values({
		id: randomUUID(),
		listingId: listing.id,
		tenantId: input.tenantId,
		entitlementId,
		amount: input.amountCents ?? 0,
		currency: input.currency ?? 'credits',
		status: 'completed',
		createdAt: now(),
		processedAt: now(),
		metadata
	});

	const entitlements = await listEntitlementsForTenant(input.tenantId);
	const record = entitlements.find((entry) => entry.id === entitlementId);
	if (!record) {
		throw new MarketplaceError('Failed to load entitlement after creation');
	}
	return record;
}

export async function getEntitlement(
	tenantId: string,
	listingId: string
): Promise<MarketplaceEntitlement | null> {
	const records = await listEntitlementsForTenant(tenantId);
	return records.find((record) => record.listingId === listingId) ?? null;
}
