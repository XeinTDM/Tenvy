<script lang="ts">
	import { cn } from '$lib/utils.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		Card,
		CardContent,
		CardDescription,
		CardFooter,
		CardHeader,
		CardTitle
	} from '$lib/components/ui/card/index.js';
	import { resolve } from '$app/paths';
	import { GitFork, Info, ShieldCheck } from '@lucide/svelte';

	import type {
		MarketplaceEntitlement,
		MarketplaceListing,
		MarketplaceStatus
	} from '$lib/data/marketplace.js';
	import type { Plugin } from '$lib/data/plugin-view.js';

	interface Props {
		listings: MarketplaceListing[];
		entitlements: MarketplaceEntitlement[];
		canPurchase?: boolean;
		canSubmitMarketplace?: boolean;
		purchaseListing: (listing: MarketplaceListing) => void | Promise<void>;
		signatureBadge: (signature: Plugin['signature']) => {
			label: string;
			icon: typeof ShieldCheck;
			class: string;
		};
		formatSignatureTime: (value: string | null | undefined) => string;
		statusStyles: Record<MarketplaceStatus, string>;
	}

	let {
		listings,
		entitlements,
		canPurchase = false,
		canSubmitMarketplace = false,
		purchaseListing,
		signatureBadge,
		formatSignatureTime,
		statusStyles
	}: Props = $props();

	function isEntitled(listingId: string): boolean {
		return entitlements.some((entry) => entry.listingId === listingId);
	}
</script>

<Card class="border-border/60">
	<CardHeader class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
		<div class="space-y-1">
			<CardTitle>Marketplace</CardTitle>
			<CardDescription>
				Deploy community plugins backed by signed releases from public repositories.
			</CardDescription>
		</div>
		<Badge variant="secondary" class="gap-2 text-xs">
			<ShieldCheck class="h-4 w-4" />
			{listings.length} listing{listings.length === 1 ? '' : 's'}
		</Badge>
	</CardHeader>
	<CardContent>
		{#if listings.length === 0}
			<p class="text-sm text-muted-foreground">
				No marketplace submissions yet. Developers can publish plugins once approved by an
				administrator.
			</p>
		{:else}
			<div class="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
				{#each listings as listing (listing.id)}
					{@const listingSignature = signatureBadge(listing.signature)}
					{@const ListingSignatureIcon = listingSignature.icon}
					<div
						class="flex flex-col justify-between rounded-lg border border-border bg-card p-4 shadow-sm"
					>
						<div class="space-y-3">
							<div class="flex items-start justify-between gap-3">
								<div class="space-y-1">
									<h3 class="text-base leading-tight font-semibold">{listing.name}</h3>
									<p class="text-xs tracking-wide text-muted-foreground uppercase">
										Version {listing.version} · {listing.pricingTier}
									</p>
								</div>
								<div class="flex flex-col items-end gap-2">
									<Badge class={statusStyles[listing.status]}>{listing.status}</Badge>
									<Badge
										variant="outline"
										class={cn(
											'flex items-center gap-1 rounded-full border px-2 py-1 text-[10px] font-semibold tracking-wide uppercase',
											listingSignature.class
										)}
									>
										<ListingSignatureIcon class="h-3 w-3" />
										{listingSignature.label}
									</Badge>
								</div>
							</div>
							<p class="text-sm leading-relaxed text-muted-foreground">
								{listing.summary ?? listing.manifest.description ?? 'No description provided.'}
							</p>
							<div class="flex flex-col gap-2 text-xs text-muted-foreground">
								<div class="flex items-center gap-2">
									<GitFork class="h-3.5 w-3.5" />
									<a
										href={resolve(listing.repositoryUrl)}
										rel="noreferrer"
										target="_blank"
										class="truncate underline decoration-dotted hover:text-foreground"
									>
										{listing.repositoryUrl}
									</a>
								</div>
								{#if listing.manifest.license}
									<div class="flex items-center gap-2">
										<ShieldCheck class="h-3.5 w-3.5" />
										<span>{listing.manifest.license.spdxId}</span>
									</div>
								{:else}
									<div class="flex items-center gap-2">
										<ShieldCheck class="h-3.5 w-3.5" />
										<span>Unlicensed</span>
									</div>
								{/if}
								<div class="flex items-center gap-2">
									<ListingSignatureIcon class="h-3.5 w-3.5" />
									<span>
										{listing.signature.signer ??
											listing.signature.publicKey ??
											listingSignature.label}
									</span>
								</div>
								<div class="flex items-center gap-2">
									<Info class="h-3.5 w-3.5" />
									<span>
										Checked {formatSignatureTime(listing.signature.checkedAt ?? null)}
									</span>
								</div>
								{#if listing.signature.error}
									<p class="text-xs text-red-500">Signature error: {listing.signature.error}</p>
								{/if}
							</div>
						</div>
						<div class="mt-4 flex items-center justify-between">
							<span class="text-xs text-muted-foreground">
								{listing.manifest.capabilities?.length ?? 0} capability{(listing.manifest
									.capabilities?.length ?? 0) === 1
									? ''
									: 'ies'}
							</span>
							<Button
								size="sm"
								variant={isEntitled(listing.id) ? 'outline' : 'default'}
								disabled={!canPurchase || isEntitled(listing.id) || listing.status !== 'approved'}
								onclick={() => purchaseListing(listing)}
							>
								{#if isEntitled(listing.id)}
									Entitled
								{:else if listing.status !== 'approved'}
									Awaiting review
								{:else}
									Purchase
								{/if}
							</Button>
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</CardContent>
	{#if canSubmitMarketplace}
		<CardFooter class="text-xs text-muted-foreground">
			Developers can submit new plugins via the controller API after uploading signed release assets
			to GitHub.
		</CardFooter>
	{/if}
</Card>
