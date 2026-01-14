import { browser } from '$app/environment';
import {
	clearStoredPorts,
	formatPortSummary,
	loadStoredPorts,
	persistPortSelection
} from '$lib/utils/rat-port-preferences.js';

const PORT_SYNC_CHANNEL_NAME = 'tenvy.rat-port-sync';
const PORT_SYNC_STORAGE_KEY = 'tenvy.rat-port-sync-message';

type PortSyncPayload =
	| { type: 'state-request'; source: string }
	| { type: 'state-update'; source: string; ports: number[]; remember: boolean }
	| { type: 'state-clear'; source: string };

type PortSyncMessage =
	| { type: 'state-request' }
	| { type: 'state-update'; ports: number[]; remember: boolean }
	| { type: 'state-clear' };

function generatePortSyncId(): string {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
		return crypto.randomUUID();
	}
	return Math.random().toString(36).slice(2);
}

export function createPortSyncStore() {
	let selectedPorts = $state<number[]>([]);
	let rememberPorts = $state(false);
	let portDialogOpen = $state(false);
	let hasHydrated = $state(false);
	
	let portSyncChannel: BroadcastChannel | null = null;
	let portSyncId: string | null = null;

	const portSummary = $derived(formatPortSummary(selectedPorts));

	function postPortSync(message: PortSyncMessage) {
		if (!browser || !portSyncId) return;
		const payload: PortSyncPayload = { ...message, source: portSyncId };
		if (portSyncChannel) {
			portSyncChannel.postMessage(payload);
			return;
		}
		try {
			window.localStorage.setItem(PORT_SYNC_STORAGE_KEY, JSON.stringify(payload));
			window.localStorage.removeItem(PORT_SYNC_STORAGE_KEY);
		} catch { }
	}

	function handleIncoming(payload: PortSyncPayload | null | undefined) {
		if (!payload || payload.source === portSyncId) return;

		if (payload.type === 'state-request') {
			if (selectedPorts.length > 0) {
				postPortSync({ type: 'state-update', ports: selectedPorts, remember: rememberPorts });
			} else {
				postPortSync({ type: 'state-clear' });
			}
			return;
		}

		if (payload.type === 'state-update') {
			selectedPorts = payload.ports;
			rememberPorts = payload.remember;
			if (payload.ports.length > 0) portDialogOpen = false;
			persistPortSelection(payload.ports, payload.remember);
			return;
		}

		if (payload.type === 'state-clear') {
			selectedPorts = [];
			rememberPorts = false;
			if (!portDialogOpen) portDialogOpen = true;
			persistPortSelection([], false);
		}
	}

	function init() {
		if (!browser) return;

		const stored = loadStoredPorts();
		if (stored) {
			selectedPorts = stored.ports;
			rememberPorts = stored.remember;
		} else {
			portDialogOpen = true;
		}

		hasHydrated = true;
		portSyncId = generatePortSyncId();

		const storageListener = (event: StorageEvent) => {
			if (event.key !== PORT_SYNC_STORAGE_KEY || !event.newValue) return;
			try { handleIncoming(JSON.parse(event.newValue)); } catch { }
		};
		window.addEventListener('storage', storageListener);

		if ('BroadcastChannel' in window) {
			portSyncChannel = new BroadcastChannel(PORT_SYNC_CHANNEL_NAME);
			portSyncChannel.addEventListener('message', (event) => handleIncoming(event.data));
		}

		queueMicrotask(() => postPortSync({ type: 'state-request' }));

		return () => {
			window.removeEventListener('storage', storageListener);
			if (portSyncChannel) {
				portSyncChannel.close();
				portSyncChannel = null;
			}
			portSyncId = null;
		};
	}

	function savePorts(ports: number[], remember: boolean) {
		selectedPorts = ports;
		rememberPorts = remember;
		persistPortSelection(ports, remember);
		postPortSync({ type: 'state-update', ports, remember });
	}

	function clearPorts() {
		clearStoredPorts();
		selectedPorts = [];
		rememberPorts = false;
		portDialogOpen = true;
		postPortSync({ type: 'state-clear' });
	}

	return {
		get selectedPorts() { return selectedPorts; },
		get rememberPorts() { return rememberPorts; },
		get portDialogOpen() { return portDialogOpen; },
		set portDialogOpen(value) { portDialogOpen = value; },
		get portSummary() { return portSummary; },
		get hasHydrated() { return hasHydrated; },
		init,
		savePorts,
		clearPorts
	};
}
