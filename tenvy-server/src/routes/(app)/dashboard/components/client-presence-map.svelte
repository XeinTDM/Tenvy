<script lang="ts">
	import type { DashboardClient } from '$lib/data/dashboard';
	import type { ClientStatus } from '$lib/data/clients';
	import { cn } from '$lib/utils.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover/index.js';
	import { onMount } from 'svelte';
	import { select } from 'd3-selection';
	import { zoom, zoomIdentity, type D3ZoomEvent, type ZoomTransform } from 'd3-zoom';
	import { geoNaturalEarth1, geoPath, geoGraticule10 } from 'd3-geo';
	import { feature } from 'topojson-client';
	import type { Feature, FeatureCollection, GeoJsonProperties, Geometry } from 'geojson';
	import type {
		GeometryCollection as TopologyGeometryCollection,
		Objects,
		Topology
	} from 'topojson-specification';
	import world from 'world-atlas/countries-50m.json';

	type MarkerStyle = { dot: string; halo: string; stroke: string };
	type Marker = {
		client: DashboardClient;
		left: number;
		top: number;
		style: MarkerStyle;
		dimmed: boolean;
	};

	const props = $props<{
		clients?: DashboardClient[];
		highlightCountry?: string | null;
	}>();

	const statusStyles: Record<ClientStatus, MarkerStyle> = {
		online: {
			dot: 'rgb(16 185 129)',
			halo: 'rgba(16, 185, 129, 0.28)',
			stroke: 'rgba(16, 185, 129, 0.55)'
		},
		idle: {
			dot: 'rgb(56 189 248)',
			halo: 'rgba(56, 189, 248, 0.26)',
			stroke: 'rgba(56, 189, 248, 0.5)'
		},
		dormant: {
			dot: 'rgb(245 158 11)',
			halo: 'rgba(245, 158, 11, 0.28)',
			stroke: 'rgba(245, 158, 11, 0.55)'
		},
		offline: {
			dot: 'rgb(148 163 184)',
			halo: 'rgba(148, 163, 184, 0.32)',
			stroke: 'rgba(148, 163, 184, 0.45)'
		}
	};

	const statusLabels: Record<ClientStatus, string> = {
		online: 'Online',
		idle: 'Idle',
		dormant: 'Dormant',
		offline: 'Offline'
	};

	const width = 860;
	const height = 460;

	type WorldProperties = GeoJsonProperties & {
		name?: string;
	};

	type WorldObjects = Objects<WorldProperties>;
	type WorldGeometryCollection = TopologyGeometryCollection<WorldProperties>;

	const topology = world as unknown as Topology<WorldObjects>;
	const countriesObject = topology.objects.countries as WorldGeometryCollection;
	const worldFeatures = feature(topology, countriesObject) as FeatureCollection<
		Geometry,
		WorldProperties
	>;

	const projection = geoNaturalEarth1().fitExtent(
		[
			[20, 20],
			[width - 20, height - 20]
		],
		worldFeatures
	);
	const pathGenerator = geoPath(projection);
	const graticulePath = pathGenerator(geoGraticule10()) ?? '';
	const landPaths = worldFeatures.features.map(
		(entry: Feature<Geometry, WorldProperties>, index: number) => {
			const baseId = entry.id ?? entry.properties?.name ?? index;
			return {
				id: String(baseId),
				key: `${baseId}-${index}`,
				d: pathGenerator(entry) ?? ''
			};
		}
	);

	const markers = $derived.by(() => {
		const highlight = props.highlightCountry ?? null;
		const clientList: DashboardClient[] = props.clients ?? [];

		return clientList
			.map((client) => {
				const { longitude, latitude, countryCode } = client.location;
				if (!Number.isFinite(longitude) || !Number.isFinite(latitude)) {
					return null;
				}

				const projected = projection([longitude, latitude]);
				if (!projected) {
					return null;
				}

				const [x, y] = projected;
				const left = clamp((x / width) * 100, -10, 110);
				const top = clamp((y / height) * 100, -10, 110);
				const dimmed = highlight !== null && (countryCode ?? null) !== highlight;

				return {
					client,
					left,
					top,
					style: statusStyles[client.status],
					dimmed
				} satisfies Marker;
			})
			.filter((value): value is Marker => value !== null);
	});

	let svgElement: SVGSVGElement | null = null;
	const identityTransform = zoomIdentity;
	let mapTransform = $state(
		`translate(${identityTransform.x}, ${identityTransform.y}) scale(${identityTransform.k})`
	);
	let overlayTransform = $state(
		`translate(${identityTransform.x}px, ${identityTransform.y}px) scale(${identityTransform.k})`
	);

	const timestampFormatter = new Intl.DateTimeFormat(undefined, {
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});

	const formatTimestamp = (value: string) => timestampFormatter.format(new Date(value));

	const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

	function updateTransforms(transform: ZoomTransform) {
		mapTransform = `translate(${transform.x}, ${transform.y}) scale(${transform.k})`;
		overlayTransform = `translate(${transform.x}px, ${transform.y}px) scale(${transform.k})`;
	}

	onMount(() => {
		if (!svgElement) {
			return;
		}

		const svgSelection = select(svgElement);
		const zoomBehavior = zoom<SVGSVGElement, unknown>()
			.scaleExtent([1, 8])
			.translateExtent([
				[-width * 0.4, -height * 0.4],
				[width * 1.4, height * 1.4]
			])
			.on('zoom', (event: D3ZoomEvent<SVGSVGElement, unknown>) => {
				updateTransforms(event.transform);
			});

		svgSelection.call(zoomBehavior).call(zoomBehavior.transform, zoomIdentity);

		return () => {
			svgSelection.on('.zoom', null);
		};
	});
</script>

<div
	role="img"
	aria-label="Client presence map"
	class="relative h-full min-h-[280px] w-full overflow-hidden rounded-xl border border-border/60 bg-linear-to-br from-background via-background to-muted/30"
>
	<svg
		bind:this={svgElement}
		viewBox={`0 0 ${width} ${height}`}
		class="h-full w-full select-none"
		preserveAspectRatio="xMidYMid meet"
	>
		<defs>
			<radialGradient id="map-glow" cx="50%" cy="50%" r="75%">
				<stop offset="0%" stop-color="rgb(15 23 42 / 0.1)" />
				<stop offset="60%" stop-color="rgb(15 23 42 / 0.04)" />
				<stop offset="100%" stop-color="rgb(15 23 42 / 0.01)" />
			</radialGradient>
		</defs>
		<rect x="0" y="0" {width} {height} fill="url(#map-glow)" />

		<g transform={mapTransform}>
			{#if graticulePath}
				<path d={graticulePath} class="fill-none stroke-border/40 stroke-[0.4]" />
			{/if}

			{#each landPaths as land (land.key)}
				{#if land.d}
					<path
						d={land.d}
						class="fill-muted/70 stroke-border/50 stroke-[0.5] dark:fill-muted/40 dark:stroke-border/40"
					/>
				{/if}
			{/each}
		</g>
	</svg>

	{#if markers.length === 0}
		<div
			class="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-muted-foreground"
		>
			No location telemetry is available for this selection.
		</div>
	{/if}

	<div class="pointer-events-none absolute inset-0">
		<div
			class="pointer-events-none absolute inset-0 origin-top-left"
			style={`transform: ${overlayTransform}; transform-origin: 0 0;`}
		>
			{#each markers as marker (marker.client.id)}
				<Popover>
					<PopoverTrigger
						type="button"
						class={cn(
							'group absolute -translate-x-1/2 -translate-y-1/2 rounded-full focus-visible:ring-2 focus-visible:ring-primary/60 focus-visible:outline-none',
							marker.dimmed ? 'opacity-30 transition-opacity hover:opacity-60' : 'opacity-100'
						)}
						style={`left: ${marker.left}%; top: ${marker.top}%; pointer-events: auto;`}
						aria-label={`${marker.client.codename} — ${marker.client.location.city}, ${marker.client.location.country}`}
					>
						<span class="relative flex h-5 w-5 items-center justify-center">
							<span
								class="absolute h-4 w-4 rounded-full transition-opacity group-hover:opacity-40"
								style={`background-color: ${marker.style.halo}; opacity: ${marker.dimmed ? 0.18 : 0.28};`}
							></span>
							<span
								class="relative h-1.5 w-1.5 rounded-full border"
								style={`background-color: ${marker.style.dot}; border-color: ${marker.style.stroke};`}
							></span>
						</span>
						<span class="sr-only">
							{marker.client.codename} — {marker.client.location.city}, {marker.client.location
								.country}
						</span>
					</PopoverTrigger>
					<PopoverContent
						side="top"
						align="center"
						sideOffset={12}
						class="w-60 space-y-3 p-4 text-sm"
					>
						<div class="space-y-1">
							<p class="text-sm leading-tight font-semibold">{marker.client.codename}</p>
							<p class="text-xs leading-tight text-muted-foreground">
								{marker.client.location.city}, {marker.client.location.country}
							</p>
						</div>
						<div class="space-y-2 text-xs text-muted-foreground">
							<div class="flex items-center justify-between gap-4">
								<span>Status</span>
								<Badge variant="secondary" class="text-[10px] tracking-wide uppercase">
									{statusLabels[marker.client.status]}
								</Badge>
							</div>
							<div class="flex items-center justify-between gap-4">
								<span>Latency</span>
								<span class="font-mono text-foreground">
									{marker.client.metrics.latencyMs} ms
								</span>
							</div>
							<div class="flex items-center justify-between gap-4">
								<span>Last seen</span>
								<span class="font-mono text-foreground">
									{formatTimestamp(marker.client.lastSeen)}
								</span>
							</div>
						</div>
					</PopoverContent>
				</Popover>
			{/each}
		</div>
	</div>
</div>
