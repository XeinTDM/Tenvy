import { RateLimiterMemory } from 'rate-limiter-flexible';

export class RateLimitError extends Error {
	public readonly status = 429;
	constructor(message = 'Too many attempts. Please slow down.') {
		super(message);
		this.name = 'RateLimitError';
	}
}

const voucherLimiter = new RateLimiterMemory({ points: 5, duration: 60 });
const webauthnLimiter = new RateLimiterMemory({ points: 10, duration: 60 });
const agentRegistrationLimiter = new RateLimiterMemory({ points: 10, duration: 60 });

async function consume(limiter: RateLimiterMemory, key: string) {
	try {
		await limiter.consume(key);
	} catch {
		throw new RateLimitError();
	}
}

export async function limitVoucherRedeem(key: string) {
	await consume(voucherLimiter, key);
}

export async function limitWebAuthn(key: string) {
	await consume(webauthnLimiter, key);
}

export async function limitAgentRegistration(key: string) {
	await consume(agentRegistrationLimiter, key);
}
