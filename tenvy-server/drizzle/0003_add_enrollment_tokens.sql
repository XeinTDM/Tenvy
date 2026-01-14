CREATE TABLE `enrollment_token` (
	`token` text PRIMARY KEY NOT NULL,
	`created_at` integer NOT NULL,
	`expires_at` integer,
	`created_by` text REFERENCES `user`(`id`) ON DELETE SET NULL,
	`max_uses` integer DEFAULT 1 NOT NULL,
	`uses` integer DEFAULT 0 NOT NULL,
	`revoked_at` integer,
	`memo` text
);
