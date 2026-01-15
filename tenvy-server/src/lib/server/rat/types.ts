import type { AgentMetadata, AgentStatus, AgentMetrics } from '../../../../../shared/types/agent';
import type { AgentConfig } from '../../../../../shared/types/config';
import type { OptionsState } from '../../../../../shared/types/options';
import type {
	Command,
	CommandResult,
	CommandOutputEvent
} from '../../../../../shared/types/messages';
import type { DownloadCatalogue } from '$lib/types/downloads';

export interface SharedNoteRecord {
	id: string;
	ciphertext: string;
	nonce: string;
	digest: string;
	version: number;
	updatedAt: Date;
}

export interface OperatorNoteRecord {
	note: string;
	tags: string[];
	updatedAt: Date | null;
	updatedBy: string | null;
}

export interface AgentSessionRecord {
	id: symbol;
	socket: WebSocket;
}

export interface AgentRecord {
	id: string;
	keyHash: string;
	metadata: AgentMetadata;
	status: AgentStatus;
	connectedAt: Date;
	lastSeen: Date;
	metrics?: AgentMetrics;
	config: AgentConfig;
	pendingCommands: Command[];
	recentResults: CommandResult[];
	sharedNotes: Map<string, SharedNoteRecord>;
	operatorNote: OperatorNoteRecord | null;
	fingerprint: string;
	sharedSecret?: string;
	session?: AgentSessionRecord;
	lastQueueDropWarning?: number;
	optionsState?: OptionsState | null;
	downloadsCatalogue: DownloadCatalogue;
	dirty?: boolean;
}

export interface CommandOutputStreamRecord {
	events: CommandOutputEvent[];
	listeners: Set<(event: CommandOutputEvent) => void>;
	completed: boolean;
	timeout?: ReturnType<typeof setTimeout>;
}

export interface SessionTokenRecord {
	hash: string;
	expiresAt: number;
}
