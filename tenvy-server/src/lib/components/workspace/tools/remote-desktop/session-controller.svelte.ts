import { browser } from '$app/environment';
import type {
	RemoteDesktopSessionState,
	RemoteDesktopSettingsPatch,
	RemoteDesktopSettings
} from '$lib/types/remote-desktop';

export interface SessionControllerOptions {
	agentId: string;
	initialSession?: RemoteDesktopSessionState | null;
}

export function createSessionController(options: SessionControllerOptions) {
	let session = $state<RemoteDesktopSessionState | null>(options.initialSession ?? null);
	let isStarting = $state(false);
	let isStopping = $state(false);
	let isUpdating = $state(false);
	let errorMessage = $state<string | null>(null);
	let infoMessage = $state<string | null>(null);

	async function refreshSession() {
		if (!browser) return session;
		try {
			const response = await fetch(`/api/agents/${options.agentId}/remote-desktop/session`);
			if (!response.ok) return session;
			const payload = (await response.json()) as { session?: RemoteDesktopSessionState | null };
			session = payload.session ?? null;
			return session;
		} catch (err) {
			console.warn('Failed to refresh remote desktop session state', err);
			return session;
		}
	}

	async function startSession(settings: RemoteDesktopSettingsPatch & { mouse: boolean; keyboard: boolean }) {
		if (!browser || isStarting) return;
		errorMessage = null;
		infoMessage = null;
		isStarting = true;
		try {
			const response = await fetch(`/api/agents/${options.agentId}/remote-desktop/session`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(settings)
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Unable to start remote desktop session';
				throw new Error(message);
			}
			const data = (await response.json()) as { session: RemoteDesktopSessionState | null };
			session = data.session ?? null;
			infoMessage = 'Remote desktop session started.';
			return session;
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to start remote desktop session';
			throw err;
		} finally {
			isStarting = false;
		}
	}

	async function stopSession(sessionId: string, options_?: { keepalive?: boolean }) {
		if (!browser || isStopping) return;
		const keepalive = options_?.keepalive === true;
		errorMessage = null;
		infoMessage = null;
		isStopping = true;
		try {
			const response = await fetch(`/api/agents/${options.agentId}/remote-desktop/session`, {
				method: 'DELETE',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ sessionId }),
				keepalive
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Unable to stop remote desktop session';
				throw new Error(message);
			}
			const data = (await response.json()) as { session: RemoteDesktopSessionState | null };
			session = data.session ?? session;
			infoMessage = 'Remote desktop session paused.';
			return session;
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to stop remote desktop session';
			throw err;
		} finally {
			isStopping = false;
		}
	}

	async function updateSession(sessionId: string, partial: RemoteDesktopSettingsPatch) {
		if (!browser || !sessionId) return;
		isUpdating = true;
		try {
			const response = await fetch(`/api/agents/${options.agentId}/remote-desktop/session`, {
				method: 'PATCH',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ sessionId, ...partial })
			});
			if (!response.ok) {
				const message = (await response.text()) || 'Failed to update session settings';
				throw new Error(message);
			}
			const data = (await response.json()) as { session: RemoteDesktopSessionState | null };
			session = data.session ?? session;
			return session;
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to update remote desktop settings';
			throw err;
		} finally {
			isUpdating = false;
		}
	}

	return {
		get session() { return session; },
		set session(value) { session = value; },
		get isStarting() { return isStarting; },
		get isStopping() { return isStopping; },
		get isUpdating() { return isUpdating; },
		get errorMessage() { return errorMessage; },
		set errorMessage(value) { errorMessage = value; },
		get infoMessage() { return infoMessage; },
		set infoMessage(value) { infoMessage = value; },
		refreshSession,
		startSession,
		stopSession,
		updateSession
	};
}
