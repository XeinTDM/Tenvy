import { existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const helperDir = dirname(fileURLToPath(import.meta.url));

const locateProjectRoot = (): string => {
	let current = helperDir;
	while (true) {
		if (existsSync(resolve(current, 'package.json'))) {
			return current;
		}
		const next = dirname(current);
		if (next === current) {
			break;
		}
		current = next;
	}
	return helperDir;
};

const projectRoot = locateProjectRoot();

export const resolveProjectPath = (...segments: string[]): string =>
	segments.length === 0 ? projectRoot : resolve(projectRoot, ...segments);

export const projectRootDirectory = projectRoot;
