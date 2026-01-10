import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';

import NotesWorkspace from './notes-workspace.svelte';
import type { Client } from '$lib/data/clients';

describe('NotesWorkspace', () => {
	const client: Client = {
		id: 'agent-123',
		codename: 'ORBIT',
		hostname: 'orbit-host',
		ip: '10.0.0.5',
		location: 'Lisbon, PT',
		os: 'Windows 11 Pro',
		platform: 'windows',
		version: '1.0.0',
		status: 'online',
		lastSeen: 'Just now',
		tags: ['vip'],
		risk: 'Medium',
		notes: '',
		noteTags: []
	};

	const originalFetch = globalThis.fetch;

	beforeEach(() => {
		vi.restoreAllMocks();
		globalThis.fetch = vi.fn();
	});

	afterEach(() => {
		vi.restoreAllMocks();
		globalThis.fetch = originalFetch;
	});

	it('loads existing notes and persists updates', async () => {
		const fetchMock = globalThis.fetch as Mock;

		fetchMock.mockResolvedValueOnce(
			new Response(
				JSON.stringify({
					note: 'Stored operator context',
					tags: ['alpha'],
					updatedAt: '2024-01-01T00:00:00.000Z',
					updatedBy: 'operator-1'
				}),
				{ status: 200 }
			)
		);

		fetchMock.mockResolvedValueOnce(
			new Response(
				JSON.stringify({
					note: 'Updated directive',
					tags: ['beta'],
					updatedAt: '2024-01-02T00:00:00.000Z',
					updatedBy: 'operator-2'
				}),
				{ status: 200 }
			)
		);

		render(NotesWorkspace, { client });

		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

		const textarea = await screen.findByLabelText(/Operational notes/i);
		expect(textarea).toHaveValue('Stored operator context');

		const tagsInput = screen.getByLabelText(/Quick tags/i);
		expect(tagsInput).toHaveValue('alpha');

		await fireEvent.input(textarea, { target: { value: 'Updated note body' } });
		await fireEvent.input(tagsInput, { target: { value: 'beta' } });

		const saveButton = screen.getByRole('button', { name: /save draft/i });
		await fireEvent.click(saveButton);

		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));

		const [, requestInit] = fetchMock.mock.calls[1];
		expect(fetchMock.mock.calls[1]?.[0]).toBe(`/api/agents/${client.id}/notes`);
		expect(requestInit?.method).toBe('POST');
		expect(requestInit?.body).toBe(JSON.stringify({ note: 'Updated note body', tags: ['beta'] }));

		expect(textarea).toHaveValue('Updated directive');
		expect(tagsInput).toHaveValue('beta');
		expect(client.notes).toBe('Updated directive');
		expect(client.noteTags).toEqual(['beta']);
		expect(client.noteUpdatedBy).toBe('operator-2');

		await screen.findByText(/Notes saved/i);
	});
});
