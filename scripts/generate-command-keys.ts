import { generateKeyPairSync } from 'crypto';

const { publicKey, privateKey } = generateKeyPairSync('ed25519');

const privateKeyBase64 = privateKey.export({ type: 'pkcs8', format: 'pem' }).toString();
const publicKeyBase64 = publicKey.export({ type: 'spki', format: 'pem' }).toString();

const publicKeyRaw = publicKey.export({ type: 'spki', format: 'der' }).slice(-32);
const privateKeyRaw = privateKey.export({ type: 'pkcs8', format: 'der' }).slice(-32);

console.log('--- COMMAND SIGNING KEYS ---');
console.log('PRIVATE_KEY (Raw Hex):', privateKeyRaw.toString('hex'));
console.log('PUBLIC_KEY (Raw Hex):', publicKeyRaw.toString('hex'));
console.log('----------------------------');
console.log('Add the PRIVATE_KEY (Raw Hex) to your .env as TENVY_COMMAND_PRIVATE_KEY');
console.log('Embed the PUBLIC_KEY (Raw Hex) in the agent.');