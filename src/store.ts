// Tiny JSON-file store for the single owner's state. Survives restarts when
// DATA_DIR is on a persistent volume; otherwise it just resets.
import fs from "node:fs";
import path from "node:path";
import type Anthropic from "@anthropic-ai/sdk";
import { config } from "./config.js";

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

export function save() {
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
