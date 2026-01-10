type LogLevel = 'info' | 'warn' | 'error' | 'debug';

interface LogEntry {
	timestamp: string;
	level: LogLevel;
	message: string;
	context?: Record<string, unknown>;
	error?: unknown;
}

class Logger {
	private level: LogLevel = (process.env.LOG_LEVEL as LogLevel) || 'info';

	private levels: Record<LogLevel, number> = {
		debug: 0,
		info: 1,
		warn: 2,
		error: 3
	};

	private shouldLog(level: LogLevel): boolean {
		return this.levels[level] >= this.levels[this.level];
	}

	private format(
		level: LogLevel,
		message: string,
		context?: Record<string, unknown>,
		error?: unknown
	): string {
		const entry: LogEntry = {
			timestamp: new Date().toISOString(),
			level,
			message,
			context,
			error: error instanceof Error ? { message: error.message, stack: error.stack } : error
		};
		return JSON.stringify(entry);
	}

	debug(message: string, context?: Record<string, unknown>) {
		if (this.shouldLog('debug')) {
			console.debug(this.format('debug', message, context));
		}
	}

	info(message: string, context?: Record<string, unknown>) {
		if (this.shouldLog('info')) {
			console.info(this.format('info', message, context));
		}
	}

	warn(message: string, context?: Record<string, unknown>, error?: unknown) {
		if (this.shouldLog('warn')) {
			console.warn(this.format('warn', message, context, error));
		}
	}

	error(message: string, context?: Record<string, unknown>, error?: unknown) {
		if (this.shouldLog('error')) {
			console.error(this.format('error', message, context, error));
		}
	}
}

export const logger = new Logger();
