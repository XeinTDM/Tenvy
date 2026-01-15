import { describe, it, expect, beforeEach } from 'vitest';
import { FileManagerStore, FileManagerError } from './file-manager';
import type { DirectoryListing, FileContent } from '$lib/types/file-manager';

describe('FileManagerStore', () => {
	let store: FileManagerStore;
	const agentId = 'agent-1';

	beforeEach(() => {
		store = new FileManagerStore({ expirationMs: -1 });
	});

	describe('ingestResource', () => {
		it('should store a directory listing', () => {
			const listing: DirectoryListing = {
				type: 'directory',
				root: 'C:/',
				path: 'C:/Windows',
				parent: 'C:/',
				entries: [
					{
						name: 'System32',
						path: 'C:/Windows/System32',
						type: 'directory',
						modifiedAt: new Date().toISOString(),
						isHidden: false,
						size: null
					}
				]
			};

			const result = store.ingestResource(agentId, listing) as DirectoryListing;
			expect(result.path).toBe('C:/Windows');
			
			const retrieved = store.getResource(agentId, 'C:/Windows') as DirectoryListing;
			expect(retrieved.entries).toHaveLength(1);
			expect(retrieved.entries[0].name).toBe('System32');
		});

		it('should store a file resource', () => {
			const file: FileContent = {
				type: 'file',
				root: 'C:/',
				path: 'C:/test.txt',
				name: 'test.txt',
				size: 4,
				modifiedAt: new Date().toISOString(),
				encoding: 'utf-8',
				content: 'test'
			};

			store.ingestResource(agentId, file);
			const retrieved = store.getResource(agentId, 'C:/test.txt') as FileContent;
			expect(retrieved.content).toBe('test');
		});

		it('should throw on invalid resource type', () => {
			expect(() => store.ingestResource(agentId, { type: 'invalid' }))
				.toThrow(FileManagerError);
		});
	});

	describe('file streaming', () => {
		it('should assemble a file from chunks', () => {
			const baseFile: FileContent = {
				type: 'file',
				root: 'C:/',
				path: 'C:/stream.bin',
				name: 'stream.bin',
				size: 10,
				modifiedAt: new Date().toISOString(),
				encoding: 'base64'
			};

			const chunk1 = Buffer.from('hello');
			const chunk2 = Buffer.from('world');

			store.ingestResource(agentId, {
				...baseFile,
				stream: { id: 's1', part: 'p1', index: 0, count: 2, offset: 0, length: 5 }
			}, chunk1);

			expect(() => store.getResource(agentId, 'C:/stream.bin')).toThrow(FileManagerError);

			store.ingestResource(agentId, {
				...baseFile,
				stream: { id: 's1', part: 'p2', index: 1, count: 2, offset: 5, length: 5 }
			}, chunk2);

			const retrieved = store.getResource(agentId, 'C:/stream.bin') as FileContent;
			expect(retrieved.content).toBe(Buffer.from('helloworld').toString('base64'));
		});

		it('should throw if chunks are out of order (start with index > 0)', () => {
			const baseFile: FileContent = {
				type: 'file',
				root: 'C:/',
				path: 'C:/stream.bin',
				name: 'stream.bin',
				size: 10,
				modifiedAt: new Date().toISOString(),
				encoding: 'base64'
			};

			expect(() => store.ingestResource(agentId, {
				...baseFile,
				stream: { id: 's1', part: 'p1', index: 1, count: 2, offset: 5, length: 5 }
			}, Buffer.from('world'))).toThrow('Out-of-order');
		});
	});

	describe('pruning', () => {
		it('should prune expired resources', async () => {
			const shortStore = new FileManagerStore({ expirationMs: 10, pruneIntervalMs: 0 });
			const listing: DirectoryListing = {
				type: 'directory',
				root: 'C:/',
				path: 'C:/',
				parent: null,
				entries: []
			};

			shortStore.ingestResource(agentId, listing);
			expect(shortStore.getResource(agentId, 'C:/')).toBeDefined();

			await new Promise(resolve => setTimeout(resolve, 20));
			expect(() => shortStore.getResource(agentId, 'C:/')).toThrow('not found');
		});
	});

    describe('removeResource', () => {
        it('should remove a stored resource', () => {
            const file: FileContent = {
				type: 'file',
				root: 'C:/',
				path: 'C:/remove.txt',
				name: 'remove.txt',
				size: 0,
				modifiedAt: new Date().toISOString(),
				encoding: 'utf-8',
				content: ''
			};
            store.ingestResource(agentId, file);
            expect(store.getResource(agentId, 'C:/remove.txt')).toBeDefined();

            store.removeResource(agentId, 'C:/remove.txt');
            expect(() => store.getResource(agentId, 'C:/remove.txt')).toThrow('not found');
        });
    });
});