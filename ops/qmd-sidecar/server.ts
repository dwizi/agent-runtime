import { spawn } from "node:child_process";
import { copyFile, lstat, mkdir, readdir, readlink, rename, rm, symlink } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";

type RunPayload = {
	workspace_dir?: unknown;
	args?: unknown;
};

const workspaceRoot = resolve((process.env.SPINNER_WORKSPACE_ROOT || "/data/workspaces").trim());
const sidecarAddr = (process.env.SPINNER_QMD_SIDECAR_ADDR || ":8091").trim();
const qmdBinary = (process.env.SPINNER_QMD_BINARY || "qmd").trim();
const qmdIndex = (process.env.SPINNER_QMD_INDEX || "spinner").trim();
const sharedModelsDir = (process.env.SPINNER_QMD_SHARED_MODELS_DIR || "").trim();
const indexTimeoutSeconds = intFromEnv("SPINNER_QMD_INDEX_TIMEOUT_SECONDS", 180);
const queryTimeoutSeconds = intFromEnv("SPINNER_QMD_QUERY_TIMEOUT_SECONDS", 30);
const maxBusyRetries = 4;

const workspaceLocks = new Map<string, Promise<void>>();
const listenConfig = parseListenAddress(sidecarAddr);

function logInfo(message: string, extra: Record<string, unknown> = {}): void {
	console.log(
		JSON.stringify({
			time: new Date().toISOString(),
			level: "INFO",
			component: "qmd-sidecar",
			msg: message,
			...extra,
		}),
	);
}

function logWarn(message: string, extra: Record<string, unknown> = {}): void {
	console.warn(
		JSON.stringify({
			time: new Date().toISOString(),
			level: "WARN",
			component: "qmd-sidecar",
			msg: message,
			...extra,
		}),
	);
}

function logError(message: string, extra: Record<string, unknown> = {}): void {
	console.error(
		JSON.stringify({
			time: new Date().toISOString(),
			level: "ERROR",
			component: "qmd-sidecar",
			msg: message,
			...extra,
		}),
	);
}

function isExpectedCollectionExistsConflict(args: string[], errorMessage: string): boolean {
	if (args.length < 2) {
		return false;
	}
	const op0 = (args[0] || "").toLowerCase();
	const op1 = (args[1] || "").toLowerCase();
	if (op0 !== "collection" || op1 !== "add") {
		return false;
	}
	const text = errorMessage.toLowerCase();
	return text.includes("already exists") && text.includes("collection");
}

function intFromEnv(name: string, fallback: number): number {
	const raw = (process.env[name] || "").trim();
	if (raw === "") {
		return fallback;
	}
	const value = Number.parseInt(raw, 10);
	return Number.isFinite(value) && value > 0 ? value : fallback;
}

function parseListenAddress(raw: string): { hostname: string; port: number } {
	if (raw === "") {
		return { hostname: "0.0.0.0", port: 8091 };
	}
	if (/^\d+$/.test(raw)) {
		return { hostname: "0.0.0.0", port: Number.parseInt(raw, 10) };
	}
	if (raw.startsWith(":")) {
		return { hostname: "0.0.0.0", port: Number.parseInt(raw.slice(1), 10) };
	}
	const index = raw.lastIndexOf(":");
	if (index < 0 || index === raw.length - 1) {
		throw new Error(`invalid sidecar address: ${raw}`);
	}
	return {
		hostname: raw.slice(0, index) || "0.0.0.0",
		port: Number.parseInt(raw.slice(index + 1), 10),
	};
}

function jsonResponse(status: number, payload: unknown): Response {
	return new Response(JSON.stringify(payload), {
		status,
		headers: { "content-type": "application/json" },
	});
}

function validateWorkspacePath(workspaceDir: string): string {
	const cleaned = resolve(workspaceDir.trim());
	const rel = relative(workspaceRoot, cleaned);
	if (rel === "" || rel === "." || rel.startsWith("..")) {
		throw new Error("workspace path must be inside workspace root");
	}
	return cleaned;
}

async function withWorkspaceLock<T>(workspaceDir: string, fn: () => Promise<T>): Promise<T> {
	const previous = workspaceLocks.get(workspaceDir) || Promise.resolve();
	let release: (() => void) | null = null;
	const current = new Promise<void>((resolvePromise) => {
		release = resolvePromise;
	});
	const chained = previous.then(() => current);
	workspaceLocks.set(workspaceDir, chained);
	await previous;
	try {
		return await fn();
	} finally {
		release?.();
		if (workspaceLocks.get(workspaceDir) === chained) {
			workspaceLocks.delete(workspaceDir);
		}
	}
}

async function ensureModelCache(homeDir: string): Promise<void> {
	const workspaceModelsDir = join(homeDir, ".cache", "qmd", "models");
	if (sharedModelsDir === "") {
		await mkdir(workspaceModelsDir, { recursive: true });
		return;
	}

	await mkdir(sharedModelsDir, { recursive: true });
	await mkdir(dirname(workspaceModelsDir), { recursive: true });

	try {
		const info = await lstat(workspaceModelsDir);
		if (info.isSymbolicLink()) {
			return;
		}
		if (!info.isDirectory()) {
			throw new Error(`qmd models path is not a directory: ${workspaceModelsDir}`);
		}
		await migrateModelCacheDir(workspaceModelsDir, sharedModelsDir);
		const remaining = await readdir(workspaceModelsDir);
		if (remaining.length === 0) {
			await rm(workspaceModelsDir, { recursive: true, force: true });
			await symlink(sharedModelsDir, workspaceModelsDir);
		}
	} catch (error) {
		if (isENOENT(error)) {
			await symlink(sharedModelsDir, workspaceModelsDir);
			return;
		}
		throw error;
	}
}

function isENOENT(error: unknown): boolean {
	return typeof error === "object" && error !== null && "code" in error && (error as { code?: string }).code === "ENOENT";
}

async function migrateModelCacheDir(sourceDir: string, destinationDir: string): Promise<void> {
	const entries = await readdir(sourceDir);
	for (const name of entries) {
		const sourcePath = join(sourceDir, name);
		const destinationPath = join(destinationDir, name);
		try {
			await rename(sourcePath, destinationPath);
			continue;
		} catch (error) {
			if (!isEXDEV(error)) {
				if (isEEXIST(error)) {
					await rm(sourcePath, { recursive: true, force: true });
					continue;
				}
				throw error;
			}
		}
		await copyPath(sourcePath, destinationPath);
		await rm(sourcePath, { recursive: true, force: true });
	}
}

function isEXDEV(error: unknown): boolean {
	return typeof error === "object" && error !== null && "code" in error && (error as { code?: string }).code === "EXDEV";
}

function isEEXIST(error: unknown): boolean {
	return typeof error === "object" && error !== null && "code" in error && (error as { code?: string }).code === "EEXIST";
}

async function copyPath(sourcePath: string, destinationPath: string): Promise<void> {
	const info = await lstat(sourcePath);
	if (info.isDirectory()) {
		await mkdir(destinationPath, { recursive: true });
		const children = await readdir(sourcePath);
		for (const child of children) {
			await copyPath(join(sourcePath, child), join(destinationPath, child));
		}
		return;
	}
	if (info.isSymbolicLink()) {
		const target = await readlink(sourcePath);
		await symlink(target, destinationPath);
		return;
	}
	if (info.isFile()) {
		await copyFile(sourcePath, destinationPath);
		return;
	}
	throw new Error(`unsupported model cache entry type: ${sourcePath}`);
}

function isRetryableQmdFailure(output: string): boolean {
	const text = output.toLowerCase();
	return (
		text.includes("database is locked") ||
		text.includes("sqlite_busy") ||
		(text.includes("enoent") && text.includes("rename") && text.includes(".ipull"))
	);
}

function timeoutFor(args: string[]): number {
	const verb = (args[0] || "").toLowerCase();
	switch (verb) {
		case "query":
		case "search":
		case "vsearch":
		case "get":
		case "status":
		case "ls":
			return queryTimeoutSeconds * 1000;
		default:
			return indexTimeoutSeconds * 1000;
	}
}

type SpawnResult = {
	output: Buffer;
	exitCode: number | null;
	signal: NodeJS.Signals | null;
	spawnError?: Error;
	timedOut: boolean;
};

async function runQMDCommand(workspaceDir: string, args: string[]): Promise<Buffer> {
	const cacheDir = join(workspaceDir, ".qmd", "cache");
	const homeDir = join(workspaceDir, ".qmd", "home");
	await mkdir(cacheDir, { recursive: true });
	await mkdir(homeDir, { recursive: true });
	await ensureModelCache(homeDir);

	const fullArgs = ["--index", qmdIndex, ...args];
	for (let attempt = 0; attempt <= maxBusyRetries; attempt += 1) {
		const result = await spawnQMD(workspaceDir, cacheDir, homeDir, fullArgs, timeoutFor(args));
		if (result.exitCode === 0 && !result.spawnError && !result.timedOut) {
			return result.output;
		}

		const outputText = result.output.toString("utf8");
		if (attempt < maxBusyRetries && isRetryableQmdFailure(outputText)) {
			const wait = (attempt + 1) * 200;
			await Bun.sleep(wait);
			continue;
		}

		if (result.spawnError) {
			throw new Error(`${qmdBinary} ${fullArgs.join(" ")}: ${result.spawnError.message}`);
		}
		if (result.timedOut) {
			throw new Error(`${qmdBinary} ${fullArgs.join(" ")}: timed out after ${Math.floor(timeoutFor(args) / 1000)}s: ${outputText.trim()}`);
		}
		throw new Error(`${qmdBinary} ${fullArgs.join(" ")}: exit status ${result.exitCode ?? 1}: ${outputText.trim()}`);
	}

	throw new Error("qmd failed after retries");
}

function spawnQMD(
	workspaceDir: string,
	cacheDir: string,
	homeDir: string,
	args: string[],
	timeoutMs: number,
): Promise<SpawnResult> {
	return new Promise<SpawnResult>((resolvePromise) => {
		const child = spawn(qmdBinary, args, {
			cwd: workspaceDir,
			env: {
				...process.env,
				NO_COLOR: "1",
				XDG_CACHE_HOME: cacheDir,
				HOME: homeDir,
			},
		});

		const chunks: Buffer[] = [];
		let spawnError: Error | undefined;
		let timedOut = false;
		const timer = setTimeout(() => {
			timedOut = true;
			child.kill("SIGKILL");
		}, timeoutMs);

		child.stdout.on("data", (chunk: Buffer | string) => {
			chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
		});
		child.stderr.on("data", (chunk: Buffer | string) => {
			chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
		});
		child.on("error", (error) => {
			spawnError = error;
		});
		child.on("close", (exitCode, signal) => {
			clearTimeout(timer);
			resolvePromise({
				output: Buffer.concat(chunks),
				exitCode,
				signal,
				spawnError,
				timedOut,
			});
		});
	});
}

async function handleRun(request: Request): Promise<Response> {
	let payload: RunPayload;
	try {
		payload = (await request.json()) as RunPayload;
	} catch {
		logWarn("invalid run payload", { reason: "invalid-json" });
		return jsonResponse(400, { error: "invalid payload" });
	}

	if (typeof payload.workspace_dir !== "string" || payload.workspace_dir.trim() === "") {
		logWarn("invalid run payload", { reason: "missing-workspace-dir" });
		return jsonResponse(400, { error: "workspace_dir is required" });
	}
	if (!Array.isArray(payload.args)) {
		logWarn("invalid run payload", { reason: "missing-args-array" });
		return jsonResponse(400, { error: "args are required" });
	}

	const args = payload.args.filter((value): value is string => typeof value === "string" && value.trim() !== "");
	if (args.length === 0) {
		logWarn("invalid run payload", { reason: "empty-args" });
		return jsonResponse(400, { error: "args are required" });
	}

	let workspaceDir: string;
	try {
		workspaceDir = validateWorkspacePath(payload.workspace_dir);
	} catch (error) {
		logWarn("invalid run payload", {
			reason: "workspace-outside-root",
			workspace_dir: payload.workspace_dir,
		});
		return jsonResponse(400, { error: error instanceof Error ? error.message : "invalid workspace path" });
	}

	const startedAt = Date.now();
	const relativeWorkspace = relative(workspaceRoot, workspaceDir);
	const command = [qmdBinary, "--index", qmdIndex, ...args].join(" ");
	logInfo("run started", {
		workspace: relativeWorkspace,
		command,
	});

	try {
		const output = await withWorkspaceLock(workspaceDir, async () => runQMDCommand(workspaceDir, args));
		logInfo("run completed", {
			workspace: relativeWorkspace,
			command,
			duration_ms: Date.now() - startedAt,
			output_bytes: output.length,
		});
		return jsonResponse(200, { output: output.toString("base64") });
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		const logPayload = {
			workspace: relativeWorkspace,
			command,
			duration_ms: Date.now() - startedAt,
			error: message,
		};
		if (isExpectedCollectionExistsConflict(args, message)) {
			logWarn("run returned expected collection conflict", logPayload);
		} else {
			logError("run failed", logPayload);
		}
		return jsonResponse(500, { error: message });
	}
}

const server = Bun.serve({
	hostname: listenConfig.hostname,
	port: listenConfig.port,
	async fetch(request) {
		const pathname = new URL(request.url).pathname;
		if (pathname === "/healthz" && request.method === "GET") {
			return jsonResponse(200, { status: "ok" });
		}
		if (pathname === "/run") {
			if (request.method !== "POST") {
				return jsonResponse(405, { error: "method not allowed" });
			}
			return handleRun(request);
		}
		return jsonResponse(404, { error: "not found" });
	},
});

console.log(
	JSON.stringify({
		time: new Date().toISOString(),
		level: "INFO",
		component: "qmd-sidecar",
		msg: "qmd sidecar listening",
		addr: `${listenConfig.hostname}:${listenConfig.port}`,
		workspace_root: workspaceRoot,
		binary: qmdBinary,
		index: qmdIndex,
	}),
);
