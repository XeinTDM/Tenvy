import { describe, it, beforeEach, afterEach, expect, vi, type Mock } from 'vitest';
import { mount } from 'svelte';
import SystemInformationDialog from './system-information-dialog.svelte';
import type { Client } from '$lib/data/clients';
import type { SystemInfoSnapshot } from '$lib/types/system-info';

const baseClient: Client = {
	id: 'agent-123',
	codename: 'FOXTROT',
	hostname: 'workstation-1',
	ip: '192.168.1.10',
	location: 'Test Lab',
	os: 'Linux',
	platform: 'linux',
	version: '2.3.4',
	status: 'online',
	lastSeen: new Date().toISOString(),
	tags: [],
	risk: 'Medium'
};

function createSnapshot(): SystemInfoSnapshot {
	const now = new Date().toISOString();
	return {
		agentId: baseClient.id,
		requestId: 'cmd-1',
		receivedAt: now,
		report: {
			collectedAt: now,
			host: {
				hostname: 'workstation-1',
				domain: 'corp.example',
				ipAddress: '10.0.0.5',
				timezone: 'UTC+00',
				bootTime: now,
				uptimeSeconds: 86_400
			},
			os: {
				platform: 'linux',
				family: 'ubuntu',
				version: '22.04',
				kernelVersion: '6.8.0-tenvy',
				kernelArch: 'x86_64',
				procs: 256,
				virtualization: 'kvm'
			},
			hardware: {
				architecture: 'amd64',
				virtualizationRole: 'guest',
				virtualizationSystem: 'kvm',
				physicalCores: 4,
				logicalCores: 8,
				cpus: [
					{
						id: 0,
						vendor: 'GenuineIntel',
						model: 'i7-9700',
						cores: 4,
						mhz: 3600,
						cacheSizeKb: 12_288
					}
				]
			},
			memory: {
				totalBytes: 16_000_000_000,
				availableBytes: 8_000_000_000,
				usedBytes: 8_000_000_000,
				usedPercent: 50,
				swapTotalBytes: 2_000_000_000,
				swapUsedBytes: 500_000_000,
				swapUsedPercent: 25
			},
			storage: [
				{
					device: '/dev/sda1',
					mountpoint: '/',
					filesystem: 'ext4',
					totalBytes: 500_000_000_000,
					usedBytes: 200_000_000_000,
					usedPercent: 40,
					readOnly: false
				}
			],
			network: [
				{
					name: 'eth0',
					mtu: 1500,
					macAddress: 'aa:bb:cc:dd:ee:ff',
					addresses: [
						{ address: '10.0.0.5', family: 'ipv4' },
						{ address: 'fe80::1', family: 'ipv6' }
					],
					flags: ['up', 'broadcast']
				}
			],
			runtime: {
				goVersion: 'go1.22.5',
				goOs: 'linux',
				goArch: 'amd64',
				logicalCpus: 8,
				goMaxProcs: 8,
				goroutines: 64,
				process: {
					pid: 4321,
					commandLine: '/opt/tenvy/agent',
					workingDirectory: '/opt/tenvy',
					createTime: now,
					cpuPercent: 1.5,
					memoryRssBytes: 120_000_000,
					memoryVmsBytes: 400_000_000
				}
			},
			environment: {
				username: 'agent',
				homeDir: '/home/agent',
				shell: '/bin/bash',
				lang: 'en_US.UTF-8',
				pathSeparator: '/',
				pathEntries: ['/usr/local/bin', '/usr/bin'],
				tempDir: '/tmp',
				environmentCount: 48
			},
			agent: {
				id: baseClient.id,
				version: baseClient.version,
				startTime: now,
				uptimeSeconds: 7200,
				tags: ['tier-1']
			},
			warnings: ['Disk usage metrics were approximated.']
		}
	};
}

describe('SystemInformationDialog (Svelte 5)', () => {
	const originalFetch = globalThis.fetch;

	beforeEach(() => {
		globalThis.fetch = vi.fn();
	});

	afterEach(() => {
		if (originalFetch) globalThis.fetch = originalFetch;
		else delete (globalThis as { fetch?: unknown }).fetch;
		document.body.innerHTML = '';
	});

	it('renders host, hardware, and runtime details from the snapshot', async () => {
		const snapshot = createSnapshot();
		const fetchMock = globalThis.fetch as Mock;

		fetchMock.mockResolvedValue({
			ok: true,
			status: 200,
			json: vi.fn().mockResolvedValue(snapshot)
		} as unknown as Response);

		mount(SystemInformationDialog, {
			target: document.body,
			props: { client: baseClient }
		});

		await Promise.resolve();
		expect(fetchMock).toHaveBeenCalledWith(`/api/agents/${baseClient.id}/system-info`);

		expect(document.body.textContent).toContain('Host overview');
		expect(document.body.textContent).toContain('corp.example');
		expect(document.body.textContent).toContain('linux');
		expect(document.body.textContent).toContain('go1.22.5');
		expect(document.body.textContent).toContain('Disk usage metrics were approximated.');
	});

	it('surfaces errors returned by the system information endpoint', async () => {
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
		fetchMock.mockResolvedValue({
			ok: false,
			status: 504,
			text: vi.fn().mockResolvedValue('Timed out waiting for agent response')
		} as unknown as Response);

		mount(SystemInformationDialog, {
			target: document.body,
			props: { client: baseClient }
		});

		await Promise.resolve();

		expect(document.body.textContent).toContain('Unable to load system information');
		expect(document.body.textContent).toContain('Timed out waiting for agent response');
	});

	it('requests a refreshed snapshot when the refresh action is triggered', async () => {
		const snapshot = createSnapshot();
		const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;

		fetchMock
			.mockResolvedValueOnce({
				ok: true,
				status: 200,
				json: vi.fn().mockResolvedValue(snapshot)
			} as unknown as Response)
			.mockResolvedValueOnce({
				ok: true,
				status: 200,
				json: vi.fn().mockResolvedValue(snapshot)
			} as unknown as Response);

		mount(SystemInformationDialog, {
			target: document.body,
			props: { client: baseClient }
		});

		await Promise.resolve();

		const refreshButton = document.querySelector(
			'button[name="Refresh snapshot"]'
		) as HTMLButtonElement;
		expect(refreshButton).toBeTruthy();

		refreshButton.click();
		await Promise.resolve();

		expect(fetchMock).toHaveBeenNthCalledWith(
			2,
			`/api/agents/${baseClient.id}/system-info?refresh=true`
		);
	});
});
