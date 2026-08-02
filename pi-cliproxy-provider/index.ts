/**
 * CLIProxyAPI provider extension for the pi coding agent.
 *
 * Fetches the model catalog from a running CLIProxyAPI instance at startup
 * and registers it as the "2api" provider. Hand-tuned metadata for known
 * models (reasoning, thinking maps, cost, compat, context/max tokens) is
 * preserved from ~/.pi/agent/models.json, while new models returned by the
 * server are added automatically.
 *
 * Configuration:
 *   - 2API_BASE_URL  base URL of the proxy (default: http://127.0.0.1:8317/v1)
 *   - 2API_API_KEY   API key (default: credential stored for "2api" in
 *                    ~/.pi/agent/auth.json, then "test-api-key")
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const PROVIDER_ID = "2api";
const DEFAULT_BASE_URL = "http://127.0.0.1:8317/v1";
const DEFAULT_API_KEY = "test-api-key";

const AGENT_DIR = join(homedir(), ".pi", "agent");
const MODELS_PATH = join(AGENT_DIR, "models.json");
const AUTH_PATH = join(AGENT_DIR, "auth.json");

interface ModelLike {
	id: string;
	name?: string;
	reasoning?: boolean;
	thinkingLevelMap?: Record<string, string | null>;
	input?: string[];
	cost?: Record<string, number>;
	contextWindow?: number;
	maxTokens?: number;
	compat?: Record<string, unknown>;
	api?: string;
	baseUrl?: string;
	headers?: Record<string, string>;
}

function readJson(path: string): unknown {
	if (!existsSync(path)) return undefined;
	return JSON.parse(readFileSync(path, "utf8"));
}

function asRecord(value: unknown): Record<string, any> | undefined {
	return typeof value === "object" && value !== null ? (value as Record<string, any>) : undefined;
}

// Prefer env var, then the stored credential for this provider, then the
// local default key configured in the proxy's config.yaml.
function resolveApiKey(): string {
	const fromEnv = process.env["2API_API_KEY"];
	if (fromEnv) return fromEnv;
	try {
		const auth = asRecord(readJson(AUTH_PATH));
		const credential = asRecord(auth?.[PROVIDER_ID]);
		if (credential?.type === "api_key" && typeof credential.key === "string") {
			return credential.key;
		}
	} catch (error) {
		console.warn(`[cliproxy] failed to read ${AUTH_PATH}:`, error);
	}
	return DEFAULT_API_KEY;
}

// Load hand-tuned model metadata from models.json so syncs never drop it.
function loadExistingModels(): { models: Map<string, ModelLike>; providerCompat: Record<string, unknown> } {
	const models = new Map<string, ModelLike>();
	let providerCompat: Record<string, unknown> = {};
	try {
		const config = asRecord(readJson(MODELS_PATH));
		const provider = asRecord(asRecord(config?.providers)?.[PROVIDER_ID]);
		const compat = asRecord(provider?.compat);
		if (compat) providerCompat = compat;
		if (Array.isArray(provider?.models)) {
			for (const model of provider.models as ModelLike[]) {
				if (model?.id) models.set(model.id, model);
			}
		}
	} catch (error) {
		console.warn(`[cliproxy] failed to read ${MODELS_PATH}:`, error);
	}
	return { models, providerCompat };
}

export default async function (pi: ExtensionAPI) {
	const baseUrl = (process.env["2API_BASE_URL"] ?? DEFAULT_BASE_URL).replace(/\/+$/, "");
	const apiKey = resolveApiKey();

	let response: Response;
	try {
		response = await fetch(`${baseUrl}/models`, {
			headers: { Authorization: `Bearer ${apiKey}` },
		});
	} catch (error) {
		console.warn(`[cliproxy] cannot reach ${baseUrl}, skipping provider registration:`, error);
		return;
	}
	if (!response.ok) {
		console.warn(`[cliproxy] GET ${baseUrl}/models failed with HTTP ${response.status}, skipping provider registration`);
		return;
	}

	const payload = (await response.json()) as { data?: Array<{ id?: string }> };
	const entries = (payload.data ?? []).filter((entry) => typeof entry.id === "string" && entry.id.length > 0);
	if (entries.length === 0) {
		console.warn(`[cliproxy] ${baseUrl}/models returned no models, skipping provider registration`);
		return;
	}

	const { models: existing, providerCompat } = loadExistingModels();

	pi.registerProvider(PROVIDER_ID, {
		name: "CLIProxyAPI (2api)",
		baseUrl,
		apiKey,
		api: "openai-completions",
		models: entries.map((entry) => {
			const prev = existing.get(entry.id as string);
			return {
				id: entry.id as string,
				name: prev?.name ?? (entry.id as string),
				reasoning: prev?.reasoning ?? true,
				input: prev?.input ?? ["text"],
				cost: prev?.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
				contextWindow: prev?.contextWindow ?? 128000,
				maxTokens: prev?.maxTokens ?? 16384,
				...(prev?.thinkingLevelMap ? { thinkingLevelMap: prev.thinkingLevelMap } : {}),
				...(prev?.api ? { api: prev.api } : {}),
				...(prev?.baseUrl ? { baseUrl: prev.baseUrl } : {}),
				...(prev?.headers ? { headers: prev.headers } : {}),
				compat: { ...providerCompat, ...(prev?.compat ?? {}) },
			};
		}),
	});
}
