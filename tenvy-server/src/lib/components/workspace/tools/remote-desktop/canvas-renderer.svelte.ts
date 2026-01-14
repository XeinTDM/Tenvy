import { browser } from '$app/environment';
import type {
	RemoteDesktopFramePacket,
	RemoteDesktopDeltaRect,
	RemoteDesktopMonitor
} from '$lib/types/remote-desktop';

export interface CanvasRendererOptions {
	onMetrics: (metrics: {
		fps?: number;
		bandwidthKbps?: number;
		width?: number;
		height?: number;
		encoderHardware?: string;
		monitors?: RemoteDesktopMonitor[];
	}) => void;
	computeLatency: (timestamp: string) => number | null;
}

const MAX_FRAME_QUEUE = 24;
const IMAGE_BASE64_PREFIX = {
	png: 'data:image/png;base64,',
	jpeg: 'data:image/jpeg;base64,'
} as const;

export function createCanvasRenderer(options: CanvasRendererOptions) {
	let canvasEl: HTMLCanvasElement | null = null;
	let canvasContext: CanvasRenderingContext2D | null = null;
	let frameQueue: RemoteDesktopFramePacket[] = [];
	let processing = false;
	let stopRequested = false;
	let imageBitmapFallbackLogged = false;
	const supportsImageBitmap = browser && typeof createImageBitmap === 'function';

	function ensureContext(): CanvasRenderingContext2D | null {
		if (!canvasEl) return null;
		if (!canvasContext) {
			const context = canvasEl.getContext('2d');
			if (!context) return null;
			context.imageSmoothingEnabled = false;
			canvasContext = context;
		}
		return canvasContext;
	}

	async function decodeBitmap(
		data: string | Uint8Array,
		mimeType: 'image/png' | 'image/jpeg'
	): Promise<ImageBitmap> {
		let blob: Blob;
		if (data instanceof Uint8Array) {
			blob = new Blob([data as unknown as BlobPart], { type: mimeType });
		} else {
			const binary = atob(data);
			const bytes = new Uint8Array(binary.length);
			for (let i = 0; i < binary.length; i += 1) {
				bytes[i] = binary.charCodeAt(i);
			}
			blob = new Blob([bytes], { type: mimeType });
		}
		return await createImageBitmap(blob);
	}

	function drawWithImageElement(
		context: CanvasRenderingContext2D,
		data: string | Uint8Array,
		x: number,
		y: number,
		width: number,
		height: number,
		encoding: 'png' | 'jpeg'
	): Promise<void> {
		return new Promise((resolve, reject) => {
			const image = new Image();
			image.decoding = 'async';
			let objectUrl: string | null = null;

			image.onload = () => {
				try {
					context.drawImage(image, x, y, width, height);
					resolve();
				} catch (err) { reject(err); }
				finally { if (objectUrl) URL.revokeObjectURL(objectUrl); }
			};
			image.onerror = () => {
				if (objectUrl) URL.revokeObjectURL(objectUrl);
				reject(new Error('Failed to decode frame image segment'));
			};

			if (data instanceof Uint8Array) {
				const mime = encoding === 'jpeg' ? 'image/jpeg' : 'image/png';
				const blob = new Blob([data as unknown as BlobPart], { type: mime });
				objectUrl = URL.createObjectURL(blob);
				image.src = objectUrl;
			} else {
				const prefix = encoding === 'jpeg' ? IMAGE_BASE64_PREFIX.jpeg : IMAGE_BASE64_PREFIX.png;
				image.src = `${prefix}${data}`;
			}
		});
	}

	async function applyFrame(frame: RemoteDesktopFramePacket) {
		const context = ensureContext();
		if (!canvasEl || !context) return;

		if (canvasEl.width !== frame.width || canvasEl.height !== frame.height) {
			canvasEl.width = frame.width;
			canvasEl.height = frame.height;
		}

		if (frame.encoding === 'clip') {
			await applyClipFrame(context, frame);
			return;
		}

		if (frame.keyFrame) {
			if (!frame.image) throw new Error('Missing key frame image data');
			const mime = frame.encoding === 'jpeg' ? 'image/jpeg' : 'image/png';
			if (supportsImageBitmap) {
				try {
					const bitmap = await decodeBitmap(frame.image, mime);
					try { context.drawImage(bitmap, 0, 0, frame.width, frame.height); }
					finally { bitmap.close(); }
					return;
				} catch (err) {
					if (!imageBitmapFallbackLogged) {
						console.warn('ImageBitmap decode failed, falling back to <img>', err);
						imageBitmapFallbackLogged = true;
					}
				}
			}
			await drawWithImageElement(context, frame.image, 0, 0, frame.width, frame.height, frame.encoding === 'jpeg' ? 'jpeg' : 'png');
			return;
		}

		if (frame.deltas && frame.deltas.length > 0) {
			for (const rect of frame.deltas) {
				await drawWithImageElement(context, rect.data, rect.x, rect.y, rect.width, rect.height, rect.encoding === 'jpeg' ? 'jpeg' : 'png');
			}
		}
	}

	async function applyClipFrame(context: CanvasRenderingContext2D, frame: RemoteDesktopFramePacket) {
		const clip = frame.clip;
		if (!clip || !clip.frames || clip.frames.length === 0) throw new Error('Missing clip frame payload');

		const start = performance.now();
		for (const segment of clip.frames) {
			const target = Math.max(0, segment.offsetMs);
			const elapsed = performance.now() - start;
			const delay = target - elapsed;
			if (delay > 1) await new Promise<void>((resolve) => setTimeout(resolve, delay));

			await drawWithImageElement(context, segment.data, 0, 0, frame.width, frame.height, segment.encoding === 'jpeg' ? 'jpeg' : 'png');
		}
	}

	async function processQueue() {
		if (processing) return;
		processing = true;
		try {
			while (frameQueue.length > 0) {
				if (stopRequested) {
					frameQueue = [];
					break;
				}
				const next = frameQueue.shift();
				if (!next) break;
				try {
					await applyFrame(next);
					options.onMetrics({
						fps: next.metrics?.fps,
						bandwidthKbps: next.metrics?.bandwidthKbps,
						width: next.width,
						height: next.height,
						encoderHardware: next.encoderHardware,
						monitors: next.monitors
					});
				} catch (err) { console.error('Failed to apply frame', err); }
			}
		} finally { processing = false; }
	}

	function enqueueFrame(frame: RemoteDesktopFramePacket) {
		if (frame.keyFrame) frameQueue = [];
		frameQueue.push(frame);
		if (frameQueue.length > MAX_FRAME_QUEUE) {
			while (frameQueue.length > MAX_FRAME_QUEUE) {
				if (frameQueue[0]?.keyFrame && frameQueue.length > 1) frameQueue.splice(1, 1);
				else frameQueue.shift();
			}
		}
		if (!processing) void processQueue();
	}

	function clear() {
		frameQueue = [];
		processing = false;
	}

	return {
		set canvasEl(el: HTMLCanvasElement | null) {
			canvasEl = el;
			canvasContext = null;
		},
		enqueueFrame,
		clear,
		set stopRequested(val: boolean) { stopRequested = val; }
	};
}
