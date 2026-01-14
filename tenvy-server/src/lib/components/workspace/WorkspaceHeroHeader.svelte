<script lang="ts">
	import type { Client } from '$lib/data/clients';
	import type { ClientToolDefinition } from '$lib/data/client-tools';

	type MetadataItem = {
		label: string;
		value: string;
		hint?: string;
	};

	let {
		client,
		tool,
		title: titleProp,
		subtitle: subtitleProp,
		metadata: metadataProp = []
	} = $props<{
		client: Client;
		tool: ClientToolDefinition;
		title?: string;
		subtitle?: string | null;
		metadata?: MetadataItem[];
	}>();

	const title = $derived(titleProp ?? tool.title);
	const subtitle = $derived(
		subtitleProp ?? (tool as ClientToolDefinition & { description?: string }).description ?? null
	);
	const metadata = $derived(metadataProp);
</script>

<header
	class="rounded-2xl border border-border/60 bg-gradient-to-r from-background via-background to-muted/40 px-6 py-5 shadow-sm"
>
	<div class="flex flex-wrap items-start justify-between gap-6">
		<div class="space-y-1">
			<h1 class="text-lg font-semibold text-foreground">{title}</h1>
			{#if subtitle}
				<p class="text-sm text-muted-foreground">{subtitle}</p>
			{/if}
			<p class="text-xs text-muted-foreground/80">
				Connected to <span class="font-semibold text-foreground">{client.hostname}</span> - {client.os}
			</p>
		</div>
		{#if metadata.length > 0}
			<div class="grid gap-3 text-right sm:grid-cols-2">
				{#each metadata as item (item.label)}
					<div class="rounded-xl border border-border/40 bg-background/70 px-4 py-2 shadow-sm">
						<p class="text-[10px] font-semibold tracking-wide text-muted-foreground/70 uppercase">
							{item.label}
						</p>
						<p class="text-sm font-semibold text-foreground">{item.value}</p>
						{#if item.hint}
							<p class="text-[10px] text-muted-foreground/70">{item.hint}</p>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</div>
</header>
