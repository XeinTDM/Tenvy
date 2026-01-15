import { createCipheriv, createDecipheriv, randomBytes, createHash } from 'crypto';

export class EncryptionManager {
	private readonly algorithm = 'aes-256-gcm';
	private readonly ivLength = 12;
	private readonly tagLength = 16;
	private readonly maxPadding = 256;

	encrypt(payload: Buffer, secretHex: string): Buffer {
		const key = this.deriveKey(secretHex);
		const iv = randomBytes(this.ivLength);
		const cipher = createCipheriv(this.algorithm, key, iv);
		
		const encrypted = Buffer.concat([cipher.update(payload), cipher.final()]);
		const tag = cipher.getAuthTag();

		const paddingLen = Math.floor(Math.random() * this.maxPadding) + 1;
		const padding = randomBytes(paddingLen);
		const paddingLenBuf = Buffer.alloc(2);
		paddingLenBuf.writeUInt16BE(paddingLen, 0);

		return Buffer.concat([iv, tag, paddingLenBuf, padding, encrypted]);
	}

	decrypt(packet: Buffer, secretHex: string): Buffer {
		const minHeader = this.ivLength + this.tagLength + 2;
		if (packet.length < minHeader) {
			throw new Error('Invalid encrypted packet: too short');
		}

		const key = this.deriveKey(secretHex);
		const iv = packet.subarray(0, this.ivLength);
		const tag = packet.subarray(this.ivLength, this.ivLength + this.tagLength);
		
		const paddingLen = packet.readUInt16BE(this.ivLength + this.tagLength);
		const ciphertextStart = minHeader + paddingLen;
		
		if (packet.length < ciphertextStart) {
			throw new Error('Invalid encrypted packet: padding length exceeds packet size');
		}

		const ciphertext = packet.subarray(ciphertextStart);

		const decipher = createDecipheriv(this.algorithm, key, iv);
		decipher.setAuthTag(tag);

		return Buffer.concat([decipher.update(ciphertext), decipher.final()]);
	}

	private deriveKey(secretHex: string): Buffer {
		return createHash('sha256').update(Buffer.from(secretHex, 'hex')).digest();
	}
}
