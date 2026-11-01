// Owner state: PostgreSQL when DATABASE_URL is set, otherwise a JSON file.
// Tiny JSON-file store for the single owner's state. Survives restarts when
// DATA_DIR is on a persistent volume; otherwise it just resets.
import fs from "node:fs";
import path from "node:path";
import type Anthropic from "@anthropic-ai/sdk";
import { config } from "./config.js";
import { loadState, saveState, migrate, sql } from "./db.js";

export interface PendingAction {
  id: string;
  createdAt: number;
  /** tool_result blocks already computed for read tools in the same assistant turn */
  readyResults: Anthropic.ToolResultBlockParam[];
  /** write tool calls waiting for the owner's confirmation */
  writes: { toolUseId: string; name: string; input: unknown; preview: unknown }[];
}

export interface Conversation {
  messages: Anthropic.MessageParam[];
  lastAt: number;
}

interface State {
  currentProjectId: string | null;
  conversation: Conversation;
  pending: PendingAction | null;
  usage: { month: string; inputTokens: number; outputTokens: number; cacheReadTokens: number };
}

const file = path.join(config.dataDir, "state.json");

function empty(): State {
  return {
    currentProjectId: null,
    conversation: { messages: [], lastAt: 0 },
    pending: null,
    usage: { month: "", inputTokens: 0, outputTokens: 0, cacheReadTokens: 0 },
  };
}

let state: State = load();

function load(): State {
  try {
    return { ...empty(), ...JSON.parse(fs.readFileSync(file, "utf8")) };
  } catch {
    return empty();
  }
}

/** Call once at startup: creates tables and loads state from Postgres when DATABASE_URL is set. */
export async function initStore() {
  if (!sql) return;
  await migrate();
  const saved = await loadState<State>();
  if (saved) state = { ...empty(), ...saved };
  else saveState(state); // first run with a DB: carry over the JSON file state
}

export function save() {
  saveState(state);
  if (sql) return;
  fs.mkdirSync(config.dataDir, { recursive: true });
  const tmp = `${file}.tmp`;
  fs.writeFileSync(tmp, JSON.stringify(state));
  fs.renameSync(tmp, file);
}

export const store = {
  get: () => state,
  update(fn: (s: State) => void) {
    fn(state);
    save();
  },
  reset() {
    state = empty();
    save();
  },
};
