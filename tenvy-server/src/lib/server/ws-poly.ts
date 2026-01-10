class PolyfilledWebSocket extends EventTarget {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSING = 2;
	static readonly CLOSED = 3;

	readonly CONNECTING = 0;
	readonly OPEN = 1;
	readonly CLOSING = 2;
	readonly CLOSED = 3;

	readyState: number = 0;
	protocol: string = '';
	extensions: string = '';
	bufferedAmount: number = 0;
	binaryType: BinaryType = 'blob';
	url: string = '';

	onopen: ((this: WebSocket, ev: Event) => void) | null = null;
	onmessage: ((this: WebSocket, ev: MessageEvent) => void) | null = null;
	onerror: ((this: WebSocket, ev: Event) => void) | null = null;
	onclose: ((this: WebSocket, ev: CloseEvent) => void) | null = null;

	constructor(private port: MessagePort) {
		super();
		this.port.onmessage = (event) => {
			const ev = new MessageEvent('message', {
				data: event.data
			});
			if (this.onmessage) this.onmessage.call(this as unknown as WebSocket, ev);
			this.dispatchEvent(ev);
		};

		this.port.onmessageerror = () => {
			const ev = new Event('error');
			if (this.onerror) this.onerror.call(this as unknown as WebSocket, ev);
			this.dispatchEvent(ev);
		};

		setTimeout(() => {
			if (this.readyState === 0) {
				this.readyState = 1;
				const ev = new Event('open');
				if (this.onopen) this.onopen.call(this as unknown as WebSocket, ev);
				this.dispatchEvent(ev);
			}
		}, 0);
	}

	send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
		if (this.readyState !== 1) {
			return;
		}
		this.port.postMessage(data);
	}

	close(code?: number, reason?: string): void {
		if (this.readyState >= 2) return;
		this.readyState = 2;
		this.port.close();
		this.readyState = 3;
		const ev = new CloseEvent('close', { code, reason, wasClean: true });
		if (this.onclose) this.onclose.call(this as unknown as WebSocket, ev);
		this.dispatchEvent(ev);
	}
}

export class WebSocketPairPolyfill {
	0: WebSocket;
	1: WebSocket;

	constructor() {
		const { port1, port2 } = new MessageChannel();
		this[0] = new PolyfilledWebSocket(port1) as unknown as WebSocket;
		this[1] = new PolyfilledWebSocket(port2) as unknown as WebSocket;
	}
}

const g = globalThis as unknown as { WebSocketPair: typeof WebSocketPairPolyfill };

if (!g.WebSocketPair) {
	g.WebSocketPair = WebSocketPairPolyfill;
}
