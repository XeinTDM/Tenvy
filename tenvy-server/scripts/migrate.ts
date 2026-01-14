import { drizzle } from 'drizzle-orm/better-sqlite3';
import { migrate } from 'drizzle-orm/better-sqlite3/migrator';
import Database from 'better-sqlite3';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const databaseUrl = process.env.DATABASE_URL || 'file:./var/tenvy.db';
const dbPath = databaseUrl.replace('file:', '');

console.log(`Migrating database at ${dbPath}...`);

const sqlite = new Database(dbPath);
const db = drizzle(sqlite);

try {
	await migrate(db, { migrationsFolder: path.join(__dirname, '../drizzle') });
	console.log('Migrations completed successfully.');
} catch (error) {
	console.error('Migration failed:', error);
	process.exit(1);
} finally {
	sqlite.close();
}
