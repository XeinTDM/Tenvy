import { defineConfig } from '@playwright/test';

export default defineConfig({
	webServer: {
		command: 'npm run build && npm run preview',
		port: 2332,
		timeout: 120000
	},
	testDir: 'e2e'
});
