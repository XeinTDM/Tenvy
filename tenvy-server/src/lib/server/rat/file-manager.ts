import type {
	DirectoryListing,
	FileContent,
	FileManagerResource,
	FileSystemEntry
} from '$lib/types/file-manager';

function isNonEmptyString(value: unknown): value is string {
	return typeof value === 'string' && value.trim().length > 0;
}

function normalizePath(path: string): string {
	const trimmed = path.trim();
	if (trimmed.length <= 1) {
		return trimmed;
	}

	if (/^[a-zA-Z]:[\\/]?$/.test(trimmed)) {
		const slash = trimmed.includes('/') ? '/' : '\\';
		return `${trimmed[0]}:${slash}`;
	}

	return trimmed.replace(/[\\/]+$/, (match, offset) => (offset === 0 ? match : ''));
}

function cloneEntry(entry: FileSystemEntry): FileSystemEntry {
	return {
		...entry,
		path: normalizePath(entry.path)
	} satisfies FileSystemEntry;
}

function cloneDirectory(listing: DirectoryListing): DirectoryListing {
	return {
		...listing,
		root: normalizePath(listing.root),
		path: normalizePath(listing.path),
		parent: listing.parent ? normalizePath(listing.parent) : listing.parent,
		entries: listing.entries.map((entry) => cloneEntry(entry))
	} satisfies DirectoryListing;
}

function cloneFile(resource: FileContent): FileContent {
	return {
		...resource,
		stream: undefined,
		root: normalizePath(resource.root),
		path: normalizePath(resource.path)
	} satisfies FileContent;
}

function assertEntry(entry: unknown): asserts entry is FileSystemEntry {
	if (!entry || typeof entry !== 'object') {
		throw new FileManagerError('Invalid file system entry payload', 400);
	}
	const candidate = entry as Partial<FileSystemEntry>;
	if (!isNonEmptyString(candidate.name)) {
		throw new FileManagerError('File system entry name is required', 400);
	}
	if (!isNonEmptyString(candidate.path)) {
		throw new FileManagerError('File system entry path is required', 400);
	}
	if (!isNonEmptyString(candidate.modifiedAt)) {
		throw new FileManagerError('File system entry modified timestamp is required', 400);
	}
	if (
		candidate.type !== 'file' &&
		candidate.type !== 'directory' &&
		candidate.type !== 'symlink' &&
		candidate.type !== 'other'
	) {
		throw new FileManagerError('Unsupported file system entry type', 400);
	}
	if (
		candidate.size !== null &&
		candidate.size !== undefined &&
		typeof candidate.size !== 'number'
	) {
		throw new FileManagerError('File system entry size must be a number or null', 400);
	}
	if (typeof candidate.isHidden !== 'boolean') {
		throw new FileManagerError('File system entry hidden flag must be boolean', 400);
	}
}

function assertDirectoryResource(resource: unknown): asserts resource is DirectoryListing {
	if (!resource || typeof resource !== 'object') {
		throw new FileManagerError('Directory listing payload is required', 400);
	}
	const listing = resource as Partial<DirectoryListing>;
	if (listing.type !== 'directory') {
		throw new FileManagerError('Invalid directory listing type', 400);
	}
	if (!isNonEmptyString(listing.root)) {
		throw new FileManagerError('Directory listing root path is required', 400);
	}
	if (!isNonEmptyString(listing.path)) {
		throw new FileManagerError('Directory listing path is required', 400);
	}
	if (
		listing.parent !== null &&
		listing.parent !== undefined &&
		!isNonEmptyString(listing.parent)
	) {
		throw new FileManagerError('Directory parent path must be a string or null', 400);
	}
	if (!Array.isArray(listing.entries)) {
		throw new FileManagerError('Directory listing entries must be an array', 400);
	}
	listing.entries.forEach(assertEntry);
}

function assertFileStream(stream: unknown): asserts stream is NonNullable<FileContent['stream']> {
	if (!stream || typeof stream !== 'object') {
		throw new FileManagerError('File stream descriptor is required', 400);
	}
	const descriptor = stream as Partial<NonNullable<FileContent['stream']>>;
	if (!isNonEmptyString(descriptor.id)) {
		throw new FileManagerError('File stream identifier is required', 400);
	}
	if (!isNonEmptyString(descriptor.part)) {
		throw new FileManagerError('File stream part identifier is required', 400);
	}
	if (!Number.isInteger(descriptor.index) || (descriptor.index ?? 0) < 0) {
		throw new FileManagerError('File stream chunk index must be a non-negative integer', 400);
	}
	if (!Number.isInteger(descriptor.count) || (descriptor.count ?? 0) <= 0) {
		throw new FileManagerError('File stream chunk count must be a positive integer', 400);
	}
	if (!Number.isInteger(descriptor.offset) || (descriptor.offset ?? 0) < 0) {
		throw new FileManagerError('File stream chunk offset must be a non-negative integer', 400);
	}
	if (!Number.isInteger(descriptor.length) || (descriptor.length ?? 0) < 0) {
		throw new FileManagerError('File stream chunk length must be a non-negative integer', 400);
	}
}

function assertFileResource(resource: unknown): asserts resource is FileContent {
	if (!resource || typeof resource !== 'object') {
		throw new FileManagerError('File content payload is required', 400);
	}
	const file = resource as Partial<FileContent>;
	if (file.type !== 'file') {
		throw new FileManagerError('Invalid file content type', 400);
	}
	if (!isNonEmptyString(file.root)) {
		throw new FileManagerError('File content root path is required', 400);
	}
	if (!isNonEmptyString(file.path)) {
		throw new FileManagerError('File content path is required', 400);
	}
	if (!isNonEmptyString(file.name)) {
		throw new FileManagerError('File content name is required', 400);
	}
	if (typeof file.size !== 'number' || Number.isNaN(file.size) || file.size < 0) {
		throw new FileManagerError('File size must be a non-negative number', 400);
	}
	if (!isNonEmptyString(file.modifiedAt)) {
		throw new FileManagerError('File modified timestamp is required', 400);
	}
	if (file.encoding !== 'utf-8' && file.encoding !== 'base64') {
		throw new FileManagerError('Unsupported file encoding', 400);
	}
	const hasStream = file.stream !== undefined && file.stream !== null;
	if (hasStream) {
		assertFileStream(file.stream);
	}
	if (!hasStream && typeof file.content !== 'string') {
		throw new FileManagerError('File content must be a string', 400);
	}
	if (file.content !== undefined && typeof file.content !== 'string') {
		throw new FileManagerError('File content must be a string when provided', 400);
	}
}

interface ResourceRecord<T extends FileManagerResource> {
	value: T;
	storedAt: Date;
}

interface PendingFileStream {
	metadata: FileContent;
	chunks: Map<number, Buffer>;
	totalChunks: number;
	updatedAt: number;
}

function ensureAgent(id: string | undefined): asserts id is string {
	if (!id || !id.trim()) {
		throw new FileManagerError('Agent identifier is required', 400);
	}
}

export class FileManagerError extends Error {
	status: number;

	constructor(message: string, status = 400) {
		super(message);
		this.name = 'FileManagerError';
		this.status = status;
	}
}

interface FileManagerStoreOptions {
	/**
	 * Milliseconds a resource remains cached before being pruned.
	 * Set to a negative value to disable pruning.
	 */
	expirationMs?: number;
	/**
	 * Minimum interval between pruning attempts per agent.
	 */
	pruneIntervalMs?: number;
}

export class FileManagerStore {
	private directories = new Map<string, Map<string, ResourceRecord<DirectoryListing>>>();

	private files = new Map<string, Map<string, ResourceRecord<FileContent>>>();

	private pendingStreams = new Map<string, Map<string, PendingFileStream>>();

	private roots = new Map<string, string>();

	private defaults = new Map<string, string>();

	private lastPruned = new Map<string, number>();

	private readonly expirationMs: number;

	private readonly pruneIntervalMs: number;

	constructor(options: FileManagerStoreOptions = {}) {
		this.expirationMs = options.expirationMs ?? 5 * 60_000;
		this.pruneIntervalMs = options.pruneIntervalMs ?? 30_000;
	}

	ingestResource(agentId: string, resource: unknown, chunk?: Buffer): FileManagerResource {
		ensureAgent(agentId);

		if (!resource || typeof resource !== 'object') {
			throw new FileManagerError('File manager resource payload is required', 400);
		}

		if ((resource as { type?: string }).type === 'directory') {
			assertDirectoryResource(resource);
			return this.storeDirectory(agentId, resource);
		}

		if ((resource as { type?: string }).type === 'file') {
			assertFileResource(resource);
			return this.storeFileResource(agentId, resource, chunk);
		}

		throw new FileManagerError('Unsupported file manager resource type', 400);
	}

	ingestResources(
		agentId: string,
		resources: { resource: unknown; chunk?: Buffer }[]
	): FileManagerResource[] {
		ensureAgent(agentId);
		if (!Array.isArray(resources)) {
			throw new FileManagerError('Resources payload must be an array', 400);
		}
		return resources.map((item) => this.ingestResource(agentId, item.resource, item.chunk));
	}

	getResource(agentId: string, path?: string | null): FileManagerResource {
		ensureAgent(agentId);

		const now = Date.now();
		this.pruneAgent(agentId, now);

		const normalized =
			typeof path === 'string' && path.trim().length > 0 ? normalizePath(path) : undefined;

		const directoryMap = this.directories.get(agentId);
		const fileMap = this.files.get(agentId);

		if (!normalized) {
			const fallback = this.defaults.get(agentId) ?? this.roots.get(agentId);
			if (!fallback) {
				throw new FileManagerError('No file manager data available for agent', 404);
			}
			const directory = directoryMap?.get(fallback);
			if (!directory) {
				throw new FileManagerError('Requested directory is not available', 404);
			}
			return cloneDirectory(directory.value);
		}

		const directory = directoryMap?.get(normalized);
		if (directory) {
			return cloneDirectory(directory.value);
		}

		const file = fileMap?.get(normalized);
		if (file) {
			return cloneFile(file.value);
		}

		throw new FileManagerError('Requested file system resource was not found', 404);
	}

	clearAgent(agentId: string): void {
		ensureAgent(agentId);
		this.directories.delete(agentId);
		this.files.delete(agentId);
		this.pendingStreams.delete(agentId);
		this.defaults.delete(agentId);
		this.roots.delete(agentId);
		this.lastPruned.delete(agentId);
	}

	removeResource(agentId: string, path: string): void {
		ensureAgent(agentId);
		if (!isNonEmptyString(path)) {
			throw new FileManagerError('Path is required for removal', 400);
		}
		const normalized = normalizePath(path);
		const directories = this.directories.get(agentId);
		const files = this.files.get(agentId);
		directories?.delete(normalized);
		files?.delete(normalized);
		this.updateDefaultDirectory(agentId, directories);
		this.cleanupAgentMaps(agentId, directories, files);
	}

	private storeDirectory(agentId: string, listing: DirectoryListing): DirectoryListing {
		const now = Date.now();
		this.pruneAgent(agentId, now, true);
		const directories =
			this.directories.get(agentId) ?? new Map<string, ResourceRecord<DirectoryListing>>();
		const cloned = cloneDirectory(listing);
		directories.set(cloned.path, { value: cloned, storedAt: new Date(now) });
		this.directories.set(agentId, directories);
		this.roots.set(agentId, cloned.root);
		this.defaults.set(agentId, cloned.path);
		return cloneDirectory(cloned);
	}

	private storeFileResource(
		agentId: string,
		resource: FileContent,
		chunk?: Buffer
	): FileManagerResource {
		if (resource.stream) {
			if (!chunk || !Buffer.isBuffer(chunk)) {
				throw new FileManagerError('File stream chunk payload is required', 400);
			}
			this.acceptStreamChunk(agentId, resource, chunk);
			return resource;
		}

		if (typeof resource.content !== 'string') {
			throw new FileManagerError('File content must be a string', 400);
		}

		return this.storeFile(agentId, resource);
	}

	private storeFile(agentId: string, resource: FileContent): FileContent {
		const now = Date.now();
		this.pruneAgent(agentId, now, true);
		const files = this.files.get(agentId) ?? new Map<string, ResourceRecord<FileContent>>();
		const cloned = cloneFile(resource);
		files.set(cloned.path, { value: cloned, storedAt: new Date(now) });
		this.files.set(agentId, files);
		if (!this.roots.has(agentId)) {
			this.roots.set(agentId, cloned.root);
		}
		return cloneFile(cloned);
	}

	private acceptStreamChunk(agentId: string, resource: FileContent, chunk: Buffer): void {
		const descriptor = resource.stream;
		if (!descriptor) {
			throw new FileManagerError('File stream descriptor is required', 400);
		}

		const streamId = descriptor.id.trim();
		if (!streamId) {
			throw new FileManagerError('File stream identifier is required', 400);
		}
		if (descriptor.index < 0 || descriptor.count <= 0 || descriptor.index >= descriptor.count) {
			throw new FileManagerError('Invalid file stream chunk metadata', 400);
		}
		if (descriptor.length < 0 || descriptor.offset < 0) {
			throw new FileManagerError('Invalid file stream chunk range', 400);
		}
		if (descriptor.offset + descriptor.length > resource.size) {
			throw new FileManagerError('File stream chunk exceeds declared file size', 400);
		}
		if (chunk.length !== descriptor.length) {
			throw new FileManagerError('File stream chunk length mismatch', 409);
		}

		let streams = this.pendingStreams.get(agentId);
		if (!streams) {
			streams = new Map<string, PendingFileStream>();
			this.pendingStreams.set(agentId, streams);
		}

		const now = Date.now();
		let pending = streams.get(streamId);
		if (!pending) {
			if (descriptor.index !== 0) {
				throw new FileManagerError('Out-of-order file stream chunk received', 409);
			}
			const metadata = cloneFile(resource);
			metadata.stream = undefined;
			metadata.content = undefined;
			metadata.encoding = 'base64';
			pending = {
				metadata,
				chunks: new Map<number, Buffer>(),
				totalChunks: descriptor.count,
				updatedAt: now
			} satisfies PendingFileStream;
			streams.set(streamId, pending);
		} else if (pending.totalChunks !== descriptor.count) {
			throw new FileManagerError('File stream chunk count mismatch', 409);
		}

		const existing = pending.chunks.get(descriptor.index);
		if (existing) {
			if (existing.length !== chunk.length) {
				throw new FileManagerError('File stream chunk length mismatch on retry', 409);
			}
			pending.updatedAt = now;
			return;
		}

		pending.chunks.set(descriptor.index, chunk);
		pending.updatedAt = now;

		if (pending.chunks.size !== pending.totalChunks) {
			return;
		}

		const ordered: Buffer[] = [];
		for (let i = 0; i < pending.totalChunks; i += 1) {
			const part = pending.chunks.get(i);
			if (!part) {
				throw new FileManagerError('Missing file stream chunk during assembly', 500);
			}
			ordered.push(part);
		}

		const combined = Buffer.concat(ordered);
		const completed: FileContent = {
			...pending.metadata,
			encoding: 'base64',
			content: combined.toString('base64')
		} satisfies FileContent;

		this.storeFile(agentId, completed);

		streams.delete(streamId);
		if (streams.size === 0) {
			this.pendingStreams.delete(agentId);
		}
	}

	private pruneAgent(agentId: string, now: number, force = false): void {
		if (this.expirationMs < 0) {
			return;
		}

		const last = this.lastPruned.get(agentId);
		if (!force && last !== undefined && now - last < this.pruneIntervalMs) {
			return;
		}

		const cutoff = now - this.expirationMs;
		const directories = this.directories.get(agentId);
		const files = this.files.get(agentId);

		const pruneMap = <T extends FileManagerResource>(
			map: Map<string, ResourceRecord<T>> | undefined
		) => {
			if (!map) {
				return undefined;
			}
			for (const [key, record] of map) {
				if (record.storedAt.getTime() <= cutoff) {
					map.delete(key);
				}
			}
			return map.size > 0 ? map : undefined;
		};

		const prunedDirectories = pruneMap(directories);
		const prunedFiles = pruneMap(files);

		const streams = this.pendingStreams.get(agentId);
		if (streams) {
			for (const [key, pending] of streams) {
				if (pending.updatedAt <= cutoff) {
					streams.delete(key);
				}
			}
			if (streams.size === 0) {
				this.pendingStreams.delete(agentId);
			}
		}

		if (prunedDirectories === undefined) {
			this.directories.delete(agentId);
		}
		if (prunedFiles === undefined) {
			this.files.delete(agentId);
		}

		const activeDirectories =
			prunedDirectories ?? (directories && directories.size > 0 ? directories : undefined);
		const activeFiles = prunedFiles ?? (files && files.size > 0 ? files : undefined);

		this.updateDefaultDirectory(agentId, activeDirectories);
		this.cleanupAgentMaps(agentId, activeDirectories, activeFiles);

		if (!this.directories.has(agentId) && !this.files.has(agentId)) {
			this.lastPruned.delete(agentId);
		} else {
			this.lastPruned.set(agentId, now);
		}
	}

	private updateDefaultDirectory(
		agentId: string,
		directories: Map<string, ResourceRecord<DirectoryListing>> | undefined
	): void {
		const currentDefault = this.defaults.get(agentId);
		if (!directories || directories.size === 0) {
			this.defaults.delete(agentId);
			return;
		}

		if (!currentDefault || !directories.has(currentDefault)) {
			const first = directories.keys().next().value as string | undefined;
			if (first) {
				this.defaults.set(agentId, first);
			}
		}
	}

	private cleanupAgentMaps(
		agentId: string,
		directories: Map<string, ResourceRecord<DirectoryListing>> | undefined,
		files: Map<string, ResourceRecord<FileContent>> | undefined
	): void {
		const hasDirectories = directories !== undefined && directories.size > 0;
		const hasFiles = files !== undefined && files.size > 0;

		if (!hasDirectories && !hasFiles) {
			this.directories.delete(agentId);
			this.files.delete(agentId);
			this.roots.delete(agentId);
			this.defaults.delete(agentId);
			this.lastPruned.delete(agentId);
		}
	}
}

export const fileManagerStore = new FileManagerStore();
