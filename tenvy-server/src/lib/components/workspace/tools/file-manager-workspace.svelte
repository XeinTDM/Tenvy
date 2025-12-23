<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { SvelteMap, SvelteURLSearchParams } from 'svelte/reactivity';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import type { Client } from '$lib/data/clients';
	import { appendWorkspaceLog, createWorkspaceLogEntry } from '$lib/workspace/utils';
	import type { WorkspaceLogEntry } from '$lib/workspace/types';
	import type {
		DirectoryListing,
		FileContent,
		FileManagerResource,
		FileOperationResponse,
		FileSystemEntry
	} from '$lib/types/file-manager';
	import {
		ArrowLeft,
		ArrowRight,
		ArrowUp,
		ArrowUpDown,
		RefreshCw,
		FolderPlus,
		FilePlus,
		Pencil,
		Trash2,
		LoaderCircle,
		Search,
		Folder,
		File as FileIcon,
		FileText,
		Link,
		ChevronDown,
		ChevronRight,
		ChevronUp
	} from '@lucide/svelte';

	const { client } = $props<{ client: Client }>();

	const fileManagerEndpoint = $derived(`/api/agents/${encodeURIComponent(client.id)}/file-manager`);

	type SortField = 'name' | 'modifiedAt' | 'type' | 'size';

	interface BreadcrumbSegment {
		label: string;
		path: string;
		isFile?: boolean;
	}

	let listing = $state<DirectoryListing | null>(null);
	let filePreview = $state<FileContent | null>(null);
	let selectedEntry = $state<FileSystemEntry | null>(null);
	let log = $state<WorkspaceLogEntry[]>([]);
	let loading = $state(false);
	let includeHidden = $state(true);
	let errorMessage = $state<string | null>(null);
	let successMessage = $state<string | null>(null);
	let newEntryName = $state('');
	let newEntryType = $state<'file' | 'directory'>('file');
	let newFileContent = $state('');
	let renameValue = $state('');
	let moveDestination = $state('');
	let editorContent = $state('');
	let editorEncoding = $state<FileContent['encoding']>('utf-8');
	let savingFile = $state(false);
	let deleting = $state(false);
	let creating = $state(false);
	let renaming = $state(false);
	let moving = $state(false);
	let rootPath = $state('');
	let searchQuery = $state('');
	let addressValue = $state('');
	let history = $state<string[]>([]);
	let historyIndex = $state(-1);
	let newEntryInputRef = $state<HTMLInputElement | null>(null);
	let renameInputRef = $state<HTMLInputElement | null>(null);
	let sortField = $state<SortField>('name');
	let sortDirection = $state<'asc' | 'desc'>('asc');

	class FileManagerRequestError extends Error {
		status: number;

		constructor(message: string, status: number) {
			super(message);
			this.name = 'FileManagerRequestError';
			this.status = status;
		}
	}

	class FileManagerResourcePendingError extends FileManagerRequestError {
		kind: 'directory' | 'file';
		path?: string;
		includeHidden?: boolean;

		constructor(
			message: string,
			kind: 'directory' | 'file',
			path: string | undefined,
			includeHidden: boolean | undefined
		) {
			super(message, 202);
			this.kind = kind;
			this.path = path;
			this.includeHidden = includeHidden;
		}
	}

	const resourcePollTimers = new SvelteMap<string, ReturnType<typeof setTimeout>>();
	const RESOURCE_POLL_INITIAL_DELAY = 600;
	const RESOURCE_POLL_MAX_DELAY = 5_000;
	const RESOURCE_POLL_BACKOFF_FACTOR = 1.5;
	const RESOURCE_POLL_MAX_ATTEMPTS = 9;
	const RESOURCE_POLL_REQUEUE_INTERVAL = 4;

	function requestKey(kind: 'directory' | 'file', path?: string | null, extras?: string): string {
		const normalized = path?.trim() ?? '';
		const suffix = extras ? `:${extras}` : '';
		return `${kind}:${normalized}${suffix}`;
	}

	function clearResourcePoll(key: string) {
		const existing = resourcePollTimers.get(key);
		if (existing !== undefined) {
			clearTimeout(existing);
			resourcePollTimers.delete(key);
		}
	}

	interface ResourcePollOptions {
		includeHidden?: boolean;
	}

	function scheduleResourcePoll(
		kind: 'directory' | 'file',
		normalizedPath: string,
		extras: string | undefined,
		options: ResourcePollOptions,
		attempt: number
	) {
		if (attempt >= RESOURCE_POLL_MAX_ATTEMPTS) {
			return;
		}

		const key = requestKey(kind, normalizedPath, extras);
		const delay =
			attempt === 0
				? RESOURCE_POLL_INITIAL_DELAY
				: Math.min(
						RESOURCE_POLL_MAX_DELAY,
						Math.floor(
							RESOURCE_POLL_INITIAL_DELAY * Math.pow(RESOURCE_POLL_BACKOFF_FACTOR, attempt)
						)
					);

		const timer = setTimeout(async () => {
			resourcePollTimers.delete(key);
			const resolvedPath = normalizedPath.length > 0 ? normalizedPath : undefined;
			try {
				const shouldRefresh = attempt > 0 && attempt % RESOURCE_POLL_REQUEUE_INTERVAL === 0;
				if (kind === 'directory') {
					await loadDirectory(resolvedPath, {
						silent: true,
						refresh: shouldRefresh,
						includeHiddenOverride: options.includeHidden
					});
				} else {
					await loadFile(normalizedPath, {
						silent: true,
						refresh: shouldRefresh
					});
				}
			} catch (err) {
				if (
					(err instanceof FileManagerRequestError && err.status === 404) ||
					err instanceof FileManagerResourcePendingError
				) {
					scheduleResourcePoll(kind, normalizedPath, extras, options, attempt + 1);
				}
			}
		}, delay);

		resourcePollTimers.set(key, timer);
	}

	function startResourcePoll(
		kind: 'directory' | 'file',
		path?: string | null,
		options: ResourcePollOptions = {}
	) {
		const normalized = path?.trim() ?? '';
		if (kind === 'file' && normalized.length === 0) {
			return;
		}

		const extras =
			options.includeHidden !== undefined
				? options.includeHidden
					? 'hidden'
					: 'visible'
				: undefined;
		const key = requestKey(kind, normalized, extras);
		clearResourcePoll(key);
		scheduleResourcePoll(kind, normalized, extras, options, 0);
	}

	function filteredEntriesList(): FileSystemEntry[] {
		const entries = listing
			? listing.entries.filter((entry) => includeHidden || !entry.isHidden)
			: [];
		const query = searchQuery.trim().toLowerCase();
		const filtered = query
			? entries.filter((entry) => entry.name.toLowerCase().includes(query))
			: entries.slice();

		const sorted = filtered.sort((a, b) => compareEntries(a, b, sortField));
		return sortDirection === 'asc' ? sorted : sorted.reverse();
	}

	const filteredEntries = $derived(filteredEntriesList());
	const breadcrumbs = $derived(buildBreadcrumbs());

	const canGoBack = $derived(historyIndex > 0);
	const canGoForward = $derived(historyIndex >= 0 && historyIndex < history.length - 1);
	const locationSummary = $derived(() => {
		if (listing?.path) {
			return listing.path;
		}
		if (filePreview?.path) {
			return filePreview.path;
		}
		if (rootPath && rootPath.trim().length > 0) {
			return rootPath;
		}
		return '—';
	});

	const sizeFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
	const dateFormatter = new Intl.DateTimeFormat(undefined, {
		dateStyle: 'medium',
		timeStyle: 'short'
	});

	function formatSize(size: number | null): string {
		if (size === null || Number.isNaN(size)) {
			return '—';
		}
		const units = ['B', 'KB', 'MB', 'GB', 'TB'];
		let value = size;
		let unit = 0;
		while (value >= 1024 && unit < units.length - 1) {
			value /= 1024;
			unit += 1;
		}
		const formatted = unit === 0 ? Math.round(value).toString() : sizeFormatter.format(value);
		return `${formatted} ${units[unit]}`;
	}

	function formatModified(value: string): string {
		try {
			return dateFormatter.format(new Date(value));
		} catch {
			return value;
		}
	}

	function parentPathOf(path: string): string {
		if (!path) {
			return rootPath;
		}
		const slashIndex = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
		if (slashIndex === -1) {
			return rootPath || path;
		}
		if (slashIndex === 2 && path[1] === ':' && path[2] === '\\') {
			return path.slice(0, slashIndex + 1);
		}
		if (slashIndex === 0) {
			return rootPath || path.slice(0, 1);
		}
		return path.slice(0, slashIndex);
	}

	function fileToEntry(resource: FileContent): FileSystemEntry {
		return {
			name: resource.name,
			path: resource.path,
			type: 'file',
			size: resource.size,
			modifiedAt: resource.modifiedAt,
			isHidden: resource.name.startsWith('.')
		} satisfies FileSystemEntry;
	}

	function typeLabel(type: FileSystemEntry['type']): string {
		switch (type) {
			case 'directory':
				return 'Folder';
			case 'file':
				return 'File';
			case 'symlink':
				return 'Symbolic link';
			default:
				return 'Other';
		}
	}

	function compareEntries(a: FileSystemEntry, b: FileSystemEntry, field: SortField): number {
		if (field === 'name') {
			const aIsDirectory = a.type === 'directory';
			const bIsDirectory = b.type === 'directory';
			if (aIsDirectory !== bIsDirectory) {
				return aIsDirectory ? -1 : 1;
			}
			return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
		}
		if (field === 'modifiedAt') {
			const aTime = Date.parse(a.modifiedAt);
			const bTime = Date.parse(b.modifiedAt);
			if (Number.isNaN(aTime) || Number.isNaN(bTime)) {
				return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
			}
			return aTime - bTime;
		}
		if (field === 'type') {
			return typeLabel(a.type).localeCompare(typeLabel(b.type), undefined, {
				sensitivity: 'base'
			});
		}
		const aSize = a.size ?? -1;
		const bSize = b.size ?? -1;
		return aSize - bSize;
	}

	function toggleSort(field: SortField) {
		if (sortField === field) {
			sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
			return;
		}
		sortField = field;
		sortDirection = 'asc';
	}

	function buildBreadcrumbs(): BreadcrumbSegment[] {
		const directoryPath =
			listing?.path ?? (filePreview ? parentPathOf(filePreview.path) : null) ?? rootPath;

		const segments: BreadcrumbSegment[] = directoryPath
			? createDirectorySegments(directoryPath)
			: [];

		if (filePreview) {
			segments.push({
				label: filePreview.name,
				path: filePreview.path,
				isFile: true
			});
		}

		return segments;
	}

	function createDirectorySegments(path: string): BreadcrumbSegment[] {
		if (/^[A-Za-z]:/.test(path)) {
			const sanitized = path.replace(/\//g, '\\');
			const parts = sanitized.split('\\').filter((part, index) => part.length > 0 || index === 0);
			if (parts.length === 0) {
				return [];
			}
			const [drivePart, ...restParts] = parts;
			const driveLabel = drivePart.endsWith(':') ? drivePart : `${drivePart}:`;
			const segments: BreadcrumbSegment[] = [{ label: driveLabel, path: `${driveLabel}\\` }];
			let current = driveLabel;
			for (const part of restParts) {
				if (!part) {
					continue;
				}
				current = `${current}\\${part}`;
				segments.push({ label: part, path: current });
			}
			return segments;
		}

		if (path.startsWith('/')) {
			const segments: BreadcrumbSegment[] = [{ label: 'Root', path: '/' }];
			let current = '';
			const parts = path.slice(1).split('/').filter(Boolean);
			for (const part of parts) {
				current = `${current}/${part}`;
				const resolved = current || '/';
				segments.push({ label: part, path: resolved });
			}
			return segments;
		}

		const segments: BreadcrumbSegment[] = [];
		const parts = path.split(/[\\/]/).filter(Boolean);
		let current = '';
		for (const part of parts) {
			current = current ? `${current}/${part}` : part;
			segments.push({ label: part, path: current });
		}
		return segments;
	}

	async function handleBreadcrumbClick(segment: BreadcrumbSegment) {
		if (!segment.path) {
			return;
		}
		try {
			if (segment.isFile) {
				await loadFile(segment.path, { select: true }).catch(() => {});
				return;
			}
			await loadDirectory(segment.path).catch(() => {});
		} catch {
			// errors handled by load helpers
		}
	}

	interface FetchResourceOptions {
		type?: 'directory' | 'file';
		refresh?: boolean;
		includeHidden?: boolean;
	}

	async function fetchResource(
		path?: string,
		options: FetchResourceOptions = {}
	): Promise<FileManagerResource> {
		const params = new SvelteURLSearchParams();
		if (path && path.trim() !== '') {
			params.set('path', path);
		}
		if (options.type) {
			params.set('type', options.type);
		}
		if (options.refresh) {
			params.set('refresh', 'true');
		}
		if (options.includeHidden !== undefined) {
			params.set('includeHidden', options.includeHidden ? 'true' : 'false');
		}
		const query = params.toString();
		const response = await fetch(`${fileManagerEndpoint}${query ? `?${query}` : ''}`);
		if (response.status === 202) {
			const detail = (await response.json().catch(() => ({}))) as {
				message?: string;
			};
			throw new FileManagerResourcePendingError(
				detail.message ||
					(options.type === 'file'
						? 'Waiting for the agent to provide the file content…'
						: 'Waiting for the agent to provide the directory listing…'),
				options.type ?? 'directory',
				path?.trim() ? path.trim() : undefined,
				options.includeHidden
			);
		}
		if (!response.ok) {
			const detail = await response.text().catch(() => '');
			throw new FileManagerRequestError(
				detail || `Request failed with status ${response.status}`,
				response.status
			);
		}
		return (await response.json()) as FileManagerResource;
	}

	function applyFilePreview(resource: FileContent) {
		filePreview = resource;
		editorEncoding = (resource.encoding ?? 'utf-8') as FileContent['encoding'];
		editorContent = resource.encoding === 'utf-8' ? (resource.content ?? '') : '';
	}

	function pushHistory(path: string) {
		const trimmed = path.trim();
		if (!trimmed) {
			return;
		}
		const next = history.slice(0, historyIndex + 1);
		if (next[next.length - 1] === trimmed) {
			history = next;
			historyIndex = next.length - 1;
			return;
		}
		next.push(trimmed);
		history = next;
		historyIndex = next.length - 1;
	}

	function findEntryByName(
		container: DirectoryListing | null,
		name: string
	): FileSystemEntry | null {
		if (!container) {
			return null;
		}
		const normalized = name.toLocaleLowerCase();
		return container.entries.find((entry) => entry.name.toLocaleLowerCase() === normalized) ?? null;
	}

	function isEntryActive(entry: FileSystemEntry): boolean {
		return selectedEntry?.path === entry.path || filePreview?.path === entry.path;
	}

	async function loadDirectory(
		path?: string,
		options: {
			silent?: boolean;
			fromHistory?: boolean;
			refresh?: boolean;
			includeHiddenOverride?: boolean;
		} = {}
	) {
		if (!options.silent) {
			loading = true;
			errorMessage = null;
			successMessage = null;
		}
		const targetPath = path ?? listing?.path ?? undefined;
		const includeHiddenPreference = options.includeHiddenOverride ?? includeHidden;
		try {
			const resource = await fetchResource(targetPath, {
				type: 'directory',
				refresh: options.refresh ?? !options.silent,
				includeHidden: includeHiddenPreference
			});
			if (resource.type !== 'directory') {
				if (!options.silent) {
					applyFilePreview(resource);
					selectedEntry =
						listing?.entries.find((entry) => entry.path === resource.path) ?? fileToEntry(resource);
					renameValue = selectedEntry ? selectedEntry.name : '';
					moveDestination = parentPathOf(resource.path);
					addressValue = resource.path;
					if (!options.fromHistory) {
						pushHistory(resource.path);
					}
				}
				return null;
			}
			listing = resource;
			rootPath = resource.root;
			addressValue = resource.path;
			if (!selectedEntry) {
				moveDestination = resource.path;
			}
			if (!options.silent) {
				filePreview = null;
				selectedEntry = null;
				renameValue = '';
				moveDestination = resource.path;
				if (!options.fromHistory) {
					pushHistory(resource.path);
				}
				successMessage = null;
			} else if (successMessage?.toLowerCase().includes('directory listing')) {
				successMessage = null;
			}
			return resource;
		} catch (err) {
			if (err instanceof FileManagerResourcePendingError) {
				if (!options.silent) {
					successMessage = err.message;
					errorMessage = null;
				}
				startResourcePoll('directory', err.path ?? targetPath ?? null, {
					includeHidden: err.includeHidden ?? includeHiddenPreference
				});
				return null;
			}
			if (err instanceof FileManagerRequestError && err.status === 404 && !options.silent) {
				startResourcePoll('directory', targetPath ?? null, {
					includeHidden: includeHiddenPreference
				});
				successMessage = 'Waiting for the agent to provide the directory listing…';
				errorMessage = null;
				return null;
			}
			if (!options.silent) {
				errorMessage = err instanceof Error ? err.message : 'Failed to load directory';
			}
			throw err instanceof Error ? err : new Error('Failed to load directory');
		} finally {
			if (!options.silent) {
				loading = false;
			}
		}
	}

	async function loadFile(
		path: string,
		options: { select?: boolean; silent?: boolean; fromHistory?: boolean; refresh?: boolean } = {}
	) {
		if (!options.silent) {
			loading = true;
			errorMessage = null;
			successMessage = null;
		}
		try {
			const resource = await fetchResource(path, {
				type: 'file',
				refresh: options.refresh ?? !options.silent
			});
			if (resource.type === 'file') {
				applyFilePreview(resource);
				if (options.select) {
					selectedEntry =
						listing?.entries.find((entry) => entry.path === resource.path) ?? fileToEntry(resource);
				} else {
					selectedEntry = fileToEntry(resource);
				}
				renameValue = selectedEntry ? selectedEntry.name : '';
				moveDestination = listing?.path ?? parentPathOf(resource.path);
				addressValue = resource.path;
				if (!options.silent && !options.fromHistory) {
					pushHistory(resource.path);
				}
				if (!options.silent) {
					successMessage = null;
				} else if (successMessage?.toLowerCase().includes('file content')) {
					successMessage = null;
				}
				return resource;
			}
			listing = resource;
			rootPath = resource.root;
			addressValue = resource.path;
			if (!options.silent) {
				filePreview = null;
				selectedEntry = null;
				renameValue = '';
				moveDestination = resource.path;
				if (!options.fromHistory) {
					pushHistory(resource.path);
				}
				successMessage = null;
			}
			return null;
		} catch (err) {
			if (err instanceof FileManagerResourcePendingError) {
				if (!options.silent) {
					successMessage = err.message;
					errorMessage = null;
				}
				startResourcePoll('file', err.path ?? path);
				return null;
			}
			if (err instanceof FileManagerRequestError && err.status === 404 && !options.silent) {
				startResourcePoll('file', path);
				successMessage = 'Waiting for the agent to provide the file content…';
				errorMessage = null;
				return null;
			}
			if (!options.silent) {
				errorMessage = err instanceof Error ? err.message : 'Failed to load file';
			}
			throw err instanceof Error ? err : new Error('Failed to load file');
		} finally {
			if (!options.silent) {
				loading = false;
			}
		}
	}

	function updateLogEntry(id: string, updates: Partial<WorkspaceLogEntry>) {
		log = log.map((entry) => (entry.id === id ? { ...entry, ...updates } : entry));
	}

	async function performOperation<T>(action: string, detail: string, fn: () => Promise<T>) {
		const entry = createWorkspaceLogEntry(action, detail, 'queued');
		log = appendWorkspaceLog(log, entry);
		updateLogEntry(entry.id, { status: 'in-progress' });
		try {
			const result = await fn();
			updateLogEntry(entry.id, { status: 'complete' });
			return result;
		} catch (err) {
			const message = err instanceof Error ? err.message : 'Unknown error';
			updateLogEntry(entry.id, {
				status: 'complete',
				detail: `${detail} — failed: ${message}`
			});
			throw err;
		}
	}

	async function handleCreateEntry() {
		if (!listing) {
			errorMessage = 'No directory selected.';
			return;
		}
		const currentListing = listing;
		const trimmed = newEntryName.trim();
		if (!trimmed) {
			errorMessage = 'Provide a name for the new entry.';
			return;
		}
		creating = true;
		successMessage = null;
		try {
			const actionLabel = newEntryType === 'file' ? 'Create file' : 'Create folder';
			const detail = `${trimmed} @ ${currentListing.path}`;
			await performOperation(actionLabel, detail, async () => {
				const response = await fetch(fileManagerEndpoint, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						action: newEntryType === 'file' ? 'create-file' : 'create-directory',
						directory: currentListing.path,
						name: trimmed,
						content: newEntryType === 'file' ? newFileContent : undefined
					})
				});
				if (!response.ok) {
					const text = await response.text().catch(() => '');
					throw new Error(text || 'Failed to create entry');
				}
				return (await response.json()) as FileOperationResponse;
			});
			await loadDirectory(currentListing.path, { silent: true }).catch(() => {});
			const created = findEntryByName(listing, trimmed);
			if (created) {
				selectEntry(created);
			}
			if (newEntryType === 'file') {
				newFileContent = '';
			}
			newEntryName = '';
			moveDestination = currentListing.path;
			errorMessage = null;
			successMessage = `${
				newEntryType === 'file' ? 'File' : 'Folder'
			} creation queued for the agent.`;
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to create entry';
		} finally {
			creating = false;
		}
	}

	async function handleRename() {
		if (!selectedEntry) {
			errorMessage = 'Select an entry to rename.';
			return;
		}
		const target = selectedEntry;
		const trimmed = renameValue.trim();
		if (!trimmed || trimmed === target.name) {
			errorMessage = 'Provide a different name to rename the entry.';
			return;
		}
		renaming = true;
		successMessage = null;
		try {
			await performOperation('Rename entry', `${target.name} → ${trimmed}`, async () => {
				const response = await fetch(fileManagerEndpoint, {
					method: 'PATCH',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						action: 'rename-entry',
						path: target.path,
						name: trimmed
					})
				});
				if (!response.ok) {
					const text = await response.text().catch(() => '');
					throw new Error(text || 'Failed to rename entry');
				}
				return (await response.json()) as FileOperationResponse;
			});
			const nextDirectory = parentPathOf(target.path);
			await loadDirectory(nextDirectory, { silent: true }).catch(() => {});
			const updated = findEntryByName(listing, trimmed);
			if (updated) {
				selectEntry(updated);
			} else {
				selectedEntry = null;
				renameValue = '';
			}
			moveDestination = listing?.path ?? nextDirectory;
			errorMessage = null;
			successMessage = 'Rename request queued for the agent.';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to rename entry';
		} finally {
			renaming = false;
		}
	}

	async function handleMove() {
		if (!selectedEntry) {
			errorMessage = 'Select an entry to move.';
			return;
		}
		const target = selectedEntry;
		const destination = moveDestination.trim();
		const resolvedDestination = (
			destination ||
			parentPathOf(target.path) ||
			listing?.path ||
			rootPath ||
			''
		).trim();
		if (!resolvedDestination) {
			errorMessage = 'Provide a destination directory.';
			return;
		}
		moving = true;
		successMessage = null;
		try {
			await performOperation('Move entry', `${target.name} → ${resolvedDestination}`, async () => {
				const response = await fetch(fileManagerEndpoint, {
					method: 'PATCH',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						action: 'move-entry',
						path: target.path,
						destination: resolvedDestination,
						name: target.name
					})
				});
				if (!response.ok) {
					const text = await response.text().catch(() => '');
					throw new Error(text || 'Failed to move entry');
				}
				return (await response.json()) as FileOperationResponse;
			});
			await loadDirectory(resolvedDestination, { silent: true }).catch(() => {});
			const updated = findEntryByName(listing, target.name);
			if (updated) {
				selectEntry(updated);
			} else {
				selectedEntry = null;
				renameValue = '';
			}
			moveDestination = resolvedDestination;
			errorMessage = null;
			successMessage = 'Move request queued for the agent.';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to move entry';
		} finally {
			moving = false;
		}
	}

	async function handleDelete() {
		if (!selectedEntry) {
			errorMessage = 'Select an entry to delete.';
			return;
		}
		const target = selectedEntry;
		deleting = true;
		successMessage = null;
		try {
			await performOperation('Delete entry', `${target.name} @ ${target.path}`, async () => {
				const response = await fetch(fileManagerEndpoint, {
					method: 'DELETE',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ path: target.path })
				});
				if (!response.ok) {
					const text = await response.text().catch(() => '');
					throw new Error(text || 'Failed to delete entry');
				}
				return (await response.json()) as FileOperationResponse;
			});
			const nextDirectory = listing?.path ?? parentPathOf(target.path);
			selectedEntry = null;
			renameValue = '';
			filePreview = null;
			await loadDirectory(nextDirectory, { silent: true }).catch(() => {});
			errorMessage = null;
			successMessage = 'Delete request queued for the agent.';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to delete entry';
		} finally {
			deleting = false;
		}
	}

	async function handleSaveFile() {
		if (!filePreview) {
			errorMessage = 'Open a file to edit its contents.';
			return;
		}
		const preview = filePreview;
		if (editorEncoding !== 'utf-8') {
			errorMessage = 'Binary files cannot be edited in text mode.';
			return;
		}
		savingFile = true;
		successMessage = null;
		try {
			await performOperation('Update file', `${preview.name} @ ${preview.path}`, async () => {
				const response = await fetch(fileManagerEndpoint, {
					method: 'PATCH',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						action: 'update-file',
						path: preview.path,
						content: editorContent
					})
				});
				if (!response.ok) {
					const text = await response.text().catch(() => '');
					throw new Error(text || 'Failed to save file');
				}
				return (await response.json()) as FileOperationResponse;
			});
			await loadFile(preview.path, { select: true, silent: true }).catch(() => {});
			await loadDirectory(parentPathOf(preview.path), { silent: true }).catch(() => {});
			errorMessage = null;
			successMessage = 'File update queued for the agent.';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to save file';
		} finally {
			savingFile = false;
		}
	}

	function selectEntry(entry: FileSystemEntry) {
		selectedEntry = entry;
		renameValue = entry.name;
		moveDestination = listing?.path ?? parentPathOf(entry.path);
		if (filePreview && filePreview.path !== entry.path) {
			filePreview = null;
		}
	}

	async function openEntry(entry: FileSystemEntry) {
		if (entry.type === 'directory') {
			await loadDirectory(entry.path).catch(() => {});
		} else {
			await loadFile(entry.path, { select: true }).catch(() => {});
		}
	}

	async function goToParent() {
		if (listing?.parent) {
			await loadDirectory(listing.parent).catch(() => {});
		}
	}

	async function refresh() {
		if (listing) {
			await loadDirectory(listing.path, { fromHistory: true }).catch(() => {});
		} else if (filePreview) {
			await loadFile(filePreview.path, { select: true, fromHistory: true }).catch(() => {});
		} else {
			await loadDirectory().catch(() => {});
		}
	}

	function clearSelection() {
		selectedEntry = null;
		renameValue = '';
		moveDestination = listing?.path ?? '';
		filePreview = null;
	}

	async function goBack() {
		if (!canGoBack) {
			return;
		}
		const targetIndex = historyIndex - 1;
		const target = history[targetIndex];
		if (!target) {
			return;
		}
		try {
			await loadDirectory(target, { fromHistory: true });
			historyIndex = targetIndex;
		} catch {
			// errors handled inside loadDirectory
		}
	}

	async function goForward() {
		if (!canGoForward) {
			return;
		}
		const targetIndex = historyIndex + 1;
		const target = history[targetIndex];
		if (!target) {
			return;
		}
		try {
			await loadDirectory(target, { fromHistory: true });
			historyIndex = targetIndex;
		} catch {
			// errors handled inside loadDirectory
		}
	}

	async function handleAddressSubmit(event: Event) {
		event.preventDefault();
		const target = addressValue.trim();
		if (!target) {
			if (listing?.path) {
				await loadDirectory(listing.path, { fromHistory: true }).catch(() => {});
			}
			return;
		}
		try {
			await loadDirectory(target);
		} catch {
			// errors handled internally
		}
	}

	function stageNewEntry(type: 'file' | 'directory') {
		if (newEntryType !== type) {
			newEntryName = '';
			if (type === 'file') {
				newFileContent = '';
			}
		}
		newEntryType = type;
		queueMicrotask(() => {
			newEntryInputRef?.focus();
			newEntryInputRef?.select();
		});
	}

	function focusRename() {
		if (!selectedEntry) {
			return;
		}
		queueMicrotask(() => {
			renameInputRef?.focus();
			renameInputRef?.select();
		});
	}

	function handleRenameKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			handleRename();
		}
	}

	function handleMoveKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			handleMove();
		}
	}

	onMount(async () => {
		try {
			await loadDirectory();
		} catch {
			// errors handled internally
		}
	});

	onDestroy(() => {
		for (const timer of resourcePollTimers.values()) {
			clearTimeout(timer);
		}
		resourcePollTimers.clear();
	});
</script>

<div class="space-y-4">
	{#if errorMessage}
		<Alert variant="destructive">
			<AlertTitle>File manager error</AlertTitle>
			<AlertDescription>{errorMessage}</AlertDescription>
		</Alert>
	{/if}

	{#if successMessage}
		<Alert>
			<AlertTitle>Request queued</AlertTitle>
			<AlertDescription>{successMessage}</AlertDescription>
		</Alert>
	{/if}

	<div class="overflow-hidden rounded-xl border bg-card text-card-foreground shadow-sm">
		<div class="flex flex-wrap items-center gap-2 border-b bg-muted/50 px-4 py-2">
			<Button
				type="button"
				variant="ghost"
				size="icon"
				aria-label="Go back"
				onclick={goBack}
				disabled={!canGoBack || loading}
			>
				<ArrowLeft class="h-4 w-4" />
			</Button>
			<Button
				type="button"
				variant="ghost"
				size="icon"
				aria-label="Go forward"
				onclick={goForward}
				disabled={!canGoForward || loading}
			>
				<ArrowRight class="h-4 w-4" />
			</Button>
			<Button
				type="button"
				variant="ghost"
				size="icon"
				aria-label="Go up"
				onclick={goToParent}
				disabled={loading || !listing?.parent}
			>
				<ArrowUp class="h-4 w-4" />
			</Button>
			<Button
				type="button"
				variant="ghost"
				size="icon"
				aria-label="Refresh"
				onclick={refresh}
				disabled={loading}
			>
				<RefreshCw class="h-4 w-4" />
			</Button>
			<div class="h-6 w-px bg-border" aria-hidden="true"></div>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				onclick={() => stageNewEntry('directory')}
				disabled={loading || !listing}
			>
				<FolderPlus class="mr-2 h-4 w-4" />
				New folder
			</Button>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				onclick={() => stageNewEntry('file')}
				disabled={loading || !listing}
			>
				<FilePlus class="mr-2 h-4 w-4" />
				New file
			</Button>
			<div class="h-6 w-px bg-border" aria-hidden="true"></div>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				onclick={focusRename}
				disabled={!selectedEntry}
			>
				<Pencil class="mr-2 h-4 w-4" />
				Rename
			</Button>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				onclick={handleDelete}
				disabled={!selectedEntry || deleting || loading}
			>
				<Trash2 class="mr-2 h-4 w-4" />
				Delete
			</Button>
			<div class="ms-auto flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
				{#if loading}
					<span class="inline-flex items-center gap-2">
						<LoaderCircle class="h-3.5 w-3.5 animate-spin" />
						Loading…
					</span>
				{/if}
				<label class="inline-flex items-center gap-2">
					<Switch bind:checked={includeHidden} />
					<span>Hidden items</span>
				</label>
			</div>
		</div>
		<div class="flex flex-wrap items-center gap-3 border-b px-4 py-3">
			<form class="flex flex-1 items-center gap-2" onsubmit={handleAddressSubmit}>
				<span class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
					Address
				</span>
				<Input
					class="flex-1"
					bind:value={addressValue}
					placeholder={rootPath || '/'}
					autocomplete="off"
				/>
				<Button type="submit" size="sm" variant="secondary" disabled={loading}>Go</Button>
			</form>
			<div class="relative w-full max-w-xs sm:ms-auto sm:w-auto">
				<Search
					class="pointer-events-none absolute top-1/2 left-2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
				/>
				<Input
					class="w-full pl-8"
					bind:value={searchQuery}
					placeholder="Search this folder"
					autocomplete="off"
					disabled={!listing}
				/>
			</div>
		</div>
		{#if breadcrumbs.length > 0}
			<nav class="flex flex-wrap items-center gap-1 border-b bg-muted/40 px-4 py-2 text-sm">
				{#each breadcrumbs as segment, index (segment.path)}
					<Button
						type="button"
						size="sm"
						variant={index === breadcrumbs.length - 1 ? 'secondary' : 'ghost'}
						class={`h-7 px-2 ${
							index === breadcrumbs.length - 1
								? 'text-secondary-foreground'
								: 'text-foreground hover:bg-muted'
						}`}
						onclick={() => handleBreadcrumbClick(segment)}
					>
						{segment.label}
					</Button>
					{#if index < breadcrumbs.length - 1}
						<ChevronRight class="h-3.5 w-3.5 text-muted-foreground" />
					{/if}
				{/each}
			</nav>
		{/if}
		<div class="grid gap-px bg-border md:grid-cols-[minmax(0,2fr)_minmax(260px,1fr)]">
			<div class="bg-background">
				<div class="max-h-[480px] overflow-auto">
					<table class="min-w-full text-sm">
						<thead
							class="sticky top-0 z-10 border-b bg-muted/60 text-xs tracking-wide text-muted-foreground uppercase"
						>
							<tr>
								<th
									class="px-3 py-2 text-left font-medium"
									aria-sort={sortField === 'name'
										? sortDirection === 'asc'
											? 'ascending'
											: 'descending'
										: 'none'}
								>
									<button
										type="button"
										class="flex items-center gap-2 font-medium text-foreground"
										onclick={() => toggleSort('name')}
									>
										<span>Name</span>
										{#if sortField === 'name'}
											{#if sortDirection === 'asc'}
												<ChevronUp class="h-3.5 w-3.5" />
											{:else}
												<ChevronDown class="h-3.5 w-3.5" />
											{/if}
										{:else}
											<ArrowUpDown class="h-3.5 w-3.5 text-muted-foreground" />
										{/if}
									</button>
								</th>
								<th
									class="px-3 py-2 text-left font-medium"
									aria-sort={sortField === 'modifiedAt'
										? sortDirection === 'asc'
											? 'ascending'
											: 'descending'
										: 'none'}
								>
									<button
										type="button"
										class="flex items-center gap-2 font-medium text-foreground"
										onclick={() => toggleSort('modifiedAt')}
									>
										<span>Date modified</span>
										{#if sortField === 'modifiedAt'}
											{#if sortDirection === 'asc'}
												<ChevronUp class="h-3.5 w-3.5" />
											{:else}
												<ChevronDown class="h-3.5 w-3.5" />
											{/if}
										{:else}
											<ArrowUpDown class="h-3.5 w-3.5 text-muted-foreground" />
										{/if}
									</button>
								</th>
								<th
									class="px-3 py-2 text-left font-medium"
									aria-sort={sortField === 'type'
										? sortDirection === 'asc'
											? 'ascending'
											: 'descending'
										: 'none'}
								>
									<button
										type="button"
										class="flex items-center gap-2 font-medium text-foreground"
										onclick={() => toggleSort('type')}
									>
										<span>Type</span>
										{#if sortField === 'type'}
											{#if sortDirection === 'asc'}
												<ChevronUp class="h-3.5 w-3.5" />
											{:else}
												<ChevronDown class="h-3.5 w-3.5" />
											{/if}
										{:else}
											<ArrowUpDown class="h-3.5 w-3.5 text-muted-foreground" />
										{/if}
									</button>
								</th>
								<th
									class="px-3 py-2 text-left font-medium"
									aria-sort={sortField === 'size'
										? sortDirection === 'asc'
											? 'ascending'
											: 'descending'
										: 'none'}
								>
									<button
										type="button"
										class="flex items-center gap-2 font-medium text-foreground"
										onclick={() => toggleSort('size')}
									>
										<span>Size</span>
										{#if sortField === 'size'}
											{#if sortDirection === 'asc'}
												<ChevronUp class="h-3.5 w-3.5" />
											{:else}
												<ChevronDown class="h-3.5 w-3.5" />
											{/if}
										{:else}
											<ArrowUpDown class="h-3.5 w-3.5 text-muted-foreground" />
										{/if}
									</button>
								</th>
							</tr>
						</thead>
						<tbody>
							{#if filteredEntries.length === 0}
								<tr>
									<td colspan="4" class="px-3 py-6 text-center text-sm text-muted-foreground">
										{#if includeHidden}
											{listing ? 'This folder is empty.' : 'No directory data available yet.'}
										{:else}
											No items match the current filters.
										{/if}
									</td>
								</tr>
							{:else}
								{#each filteredEntries as entry (entry.path)}
									<tr
										class={`cursor-pointer border-b border-border/60 transition ${
											isEntryActive(entry)
												? 'bg-primary/5 text-foreground'
												: 'bg-background hover:bg-muted/40'
										}`}
										onclick={() => selectEntry(entry)}
										ondblclick={() => openEntry(entry)}
									>
										<td class="px-3 py-2">
											<div class="flex items-center gap-2">
												{#if entry.type === 'directory'}
													<Folder class="h-4 w-4 text-muted-foreground" />
												{:else if entry.type === 'file'}
													<FileIcon class="h-4 w-4 text-muted-foreground" />
												{:else if entry.type === 'symlink'}
													<Link class="h-4 w-4 text-muted-foreground" />
												{:else}
													<FileText class="h-4 w-4 text-muted-foreground" />
												{/if}
												<span class="flex-1 truncate font-medium text-foreground">
													{entry.name}
												</span>
												{#if entry.isHidden}
													<span class="text-xs text-muted-foreground">hidden</span>
												{/if}
											</div>
										</td>
										<td class="px-3 py-2 text-muted-foreground">
											{formatModified(entry.modifiedAt)}
										</td>
										<td class="px-3 py-2 text-muted-foreground">{typeLabel(entry.type)}</td>
										<td class="px-3 py-2 text-muted-foreground">{formatSize(entry.size)}</td>
									</tr>
								{/each}
							{/if}
						</tbody>
					</table>
				</div>
			</div>
			<div class="space-y-6 bg-muted/20 p-4">
				<section class="space-y-3">
					<div class="flex items-center justify-between">
						<h3 class="text-sm font-semibold text-foreground">Details</h3>
						{#if selectedEntry}
							<span class="text-xs text-muted-foreground">{typeLabel(selectedEntry.type)}</span>
						{/if}
					</div>
					{#if selectedEntry}
						<div
							class="grid gap-1 rounded-lg border border-border/60 bg-background/60 p-3 font-mono text-xs"
						>
							<p class="truncate text-foreground">{selectedEntry.path}</p>
							<p>Size: {formatSize(selectedEntry.size)}</p>
							<p>Modified: {formatModified(selectedEntry.modifiedAt)}</p>
						</div>
						<div class="grid gap-2">
							<Label for="rename-entry">Name</Label>
							<Input
								id="rename-entry"
								bind:value={renameValue}
								autocomplete="off"
								bind:ref={renameInputRef}
								onkeydown={handleRenameKeydown}
							/>
							<Button
								type="button"
								size="sm"
								variant="secondary"
								onclick={handleRename}
								disabled={renaming || loading}
							>
								{renaming ? 'Renaming…' : 'Apply rename'}
							</Button>
						</div>
						<div class="grid gap-2">
							<Label for="move-entry">Move to</Label>
							<Input
								id="move-entry"
								bind:value={moveDestination}
								placeholder={rootPath || '/'}
								autocomplete="off"
								onkeydown={handleMoveKeydown}
							/>
							<Button
								type="button"
								size="sm"
								variant="secondary"
								onclick={handleMove}
								disabled={moving || loading}
							>
								{moving ? 'Moving…' : 'Move item'}
							</Button>
						</div>
						<div class="flex flex-wrap gap-2">
							<Button type="button" variant="ghost" size="sm" onclick={clearSelection}>
								Clear selection
							</Button>
							<Button
								type="button"
								variant="destructive"
								size="sm"
								onclick={handleDelete}
								disabled={deleting || loading}
							>
								{deleting ? 'Deleting…' : 'Delete'}
							</Button>
						</div>
					{:else}
						<p class="text-sm text-muted-foreground">
							Select an item to inspect its properties or double-click to open it.
						</p>
					{/if}
				</section>
				<section class="space-y-3">
					<div class="flex items-center justify-between">
						<h3 class="text-sm font-semibold text-foreground">New item</h3>
						{#if listing}
							<span class="max-w-[180px] truncate text-right text-xs text-muted-foreground">
								{listing.path}
							</span>
						{/if}
					</div>
					<form
						class="space-y-3"
						onsubmit={(event) => {
							event.preventDefault();
							handleCreateEntry();
						}}
					>
						<div class="grid gap-2">
							<Label for="new-entry-name">Name</Label>
							<Input
								id="new-entry-name"
								bind:value={newEntryName}
								autocomplete="off"
								placeholder={newEntryType === 'file' ? 'document.txt' : 'New folder'}
								bind:ref={newEntryInputRef}
							/>
						</div>
						<div class="flex flex-wrap items-center gap-2">
							<span class="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
								Type
							</span>
							<div class="flex gap-2">
								<Button
									type="button"
									size="sm"
									variant={newEntryType === 'file' ? 'secondary' : 'outline'}
									onclick={() => stageNewEntry('file')}
								>
									<FilePlus class="mr-2 h-4 w-4" />
									File
								</Button>
								<Button
									type="button"
									size="sm"
									variant={newEntryType === 'directory' ? 'secondary' : 'outline'}
									onclick={() => stageNewEntry('directory')}
								>
									<FolderPlus class="mr-2 h-4 w-4" />
									Folder
								</Button>
							</div>
						</div>
						{#if newEntryType === 'file'}
							<div class="grid gap-2">
								<Label for="new-entry-content">Initial content</Label>
								<Textarea
									id="new-entry-content"
									bind:value={newFileContent}
									class="h-32 font-mono text-xs"
									placeholder="Optional initial file contents"
								/>
							</div>
						{/if}
						<Button type="submit" disabled={creating || loading || !listing}>
							{creating ? 'Creating…' : `Create ${newEntryType === 'file' ? 'file' : 'folder'}`}
						</Button>
					</form>
				</section>
				{#if filePreview}
					<section class="space-y-3">
						<div class="flex items-center justify-between">
							<h3 class="text-sm font-semibold text-foreground">Preview</h3>
							<span class="text-xs text-muted-foreground">
								{filePreview.encoding === 'utf-8' ? 'Text' : 'Binary'}
							</span>
						</div>
						<div
							class="grid gap-1 rounded-lg border border-border/60 bg-background/60 p-3 text-xs text-muted-foreground"
						>
							<p class="truncate text-foreground">{filePreview.path}</p>
							<p>Size: {formatSize(filePreview.size)}</p>
							<p>Modified: {formatModified(filePreview.modifiedAt)}</p>
						</div>
						{#if filePreview.encoding === 'utf-8'}
							<Textarea
								bind:value={editorContent}
								class="h-48 font-mono text-xs"
								spellcheck={false}
							/>
							<Button type="button" onclick={handleSaveFile} disabled={savingFile || loading}>
								{savingFile ? 'Saving…' : 'Save changes'}
							</Button>
						{:else}
							<div class="rounded-lg border border-border/60 bg-background/60 p-3 text-xs">
								<p class="text-muted-foreground">
									Binary files are displayed as base64 for inspection and cannot be edited.
								</p>
								<div
									class="mt-2 max-h-48 overflow-auto rounded border border-border/40 bg-background/80 p-3 font-mono"
								>
									<pre
										class="text-xs break-all whitespace-pre-wrap text-muted-foreground">{filePreview.content}</pre>
								</div>
							</div>
						{/if}
					</section>
				{/if}
			</div>
		</div>
		<div
			class="flex flex-wrap items-center gap-3 border-t bg-muted/50 px-4 py-2 text-xs text-muted-foreground"
		>
			<span>{filteredEntries.length} {filteredEntries.length === 1 ? 'item' : 'items'}</span>
			<span>
				Selected:
				{selectedEntry ? selectedEntry.name : filePreview ? filePreview.name : 'None'}
			</span>
			<span class="ms-auto truncate">
				Location: {locationSummary}
			</span>
		</div>
	</div>
</div>
