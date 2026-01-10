declare module '@simplewebauthn/server' {
	export const generateRegistrationOptions: (...args: unknown[]) => Promise<unknown>;
	export const verifyRegistrationResponse: (...args: unknown[]) => Promise<unknown>;
	export const generateAuthenticationOptions: (...args: unknown[]) => Promise<unknown>;
	export const verifyAuthenticationResponse: (...args: unknown[]) => Promise<unknown>;
}

declare module '@simplewebauthn/browser' {
	export const startRegistration: (...args: unknown[]) => Promise<unknown>;
	export const startAuthentication: (...args: unknown[]) => Promise<unknown>;
}

declare module '$lib/paraglide/server' {
	export function paraglideMiddleware(
		request: Request,
		handler: (args: { request: Request; locale: string }) => Response | Promise<Response>
	): Promise<Response>;
}

declare module '$lib/paraglide/runtime' {
	export function deLocalizeUrl(url: URL): URL;
}

declare module 'systeminformation' {
	const value: unknown;
	export default value;
}

declare module 'tar-stream' {
	import type { Readable } from 'node:stream';

	interface TarEntryHeader {
		name?: string;
		size?: number;
		mode?: number;
		mtime?: Date;
		type?: string;
	}

	type EntryCallback = (error?: Error | null) => void;

	interface TarEntryStream extends Readable {
		on(event: 'error', listener: (error: Error) => void): this;
	}

	interface PackStream {
		entry(header: TarEntryHeader, buffer: Buffer | Uint8Array | string, cb?: EntryCallback): void;
		entry(header: TarEntryHeader, cb: EntryCallback): void;
		finalize(): void;
		on(event: 'data', listener: (chunk: Buffer) => void): this;
		on(event: 'end', listener: () => void): this;
		on(event: 'error', listener: (error: Error) => void): this;
	}

	interface ExtractStream extends NodeJS.WritableStream {
		on(
			event: 'entry',
			listener: (header: TarEntryHeader, stream: TarEntryStream, next: () => void) => void
		): this;
		on(event: 'finish', listener: () => void): this;
		on(event: 'error', listener: (error: Error) => void): this;
		destroy(error?: Error): this;
	}

	export function pack(): PackStream;
	export function extract(): ExtractStream;
}
