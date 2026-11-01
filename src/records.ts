// Things the bot keeps that the task tool doesn't: risks, decisions, change requests,
// payment milestones (with TOR deliverables), reminders, man-day budgets and effort logs.
// Stored in Postgres (pm_records), tagged with the active workspace.
import { sql, workspace } from "./db.js";

export type RecordKind = "risk" | "decision" | "cr" | "milestone" | "reminder" | "budget" | "effort";

export interface RiskData { title: string; detail?: string; impact: Level; likelihood: Level; owner?: string; mitigation?: string }
export interface DecisionData { title: string; detail?: string; source?: string; date: string }
export interface CrData {
  title: string; detail?: string; requester?: string; requestedOn?: string;
  tasks?: number; manDays?: number; milestoneId?: number | null; impactNote?: string; notionUrl?: string;
}
export interface Deliverable { tor?: string; name: string; itemKey?: string }
export interface MilestoneData { name: string; due: string; valuePercent?: number; modules?: string[]; deliverables: Deliverable[] }
export interface ReminderData { text: string; itemKey?: string; at: string }
export interface BudgetData { totalManDays: number; modules: Record<string, number> }
export interface EffortData { who?: string; itemKey?: string; hours: number; date: string; note?: string }

export type Level = "high" | "medium" | "low";

export interface Rec<T = unknown> {
  id: number; kind: RecordKind; code: string | null; project_id: string | null; status: string;
  data: T; due_at: Date | null; created_at: Date; updated_at: Date;
}

const PREFIX: Partial<Record<RecordKind, string>> = { risk: "R", decision: "D", cr: "CR", milestone: "M" };

function db() {
  if (!sql) throw new Error("ฟีเจอร์นี้ต้องใช้ฐานข้อมูล PostgreSQL");
  return sql;
}

export const recordsEnabled = () => !!sql;

export async function addRecord<T>(kind: RecordKind, projectId: string | null, data: T, opts: { status?: string; dueAt?: Date | null } = {}): Promise<Rec<T>> {
  const s = db();
  const ws = workspace();
  let code: string | null = null;
  const pre = PREFIX[kind];
  if (pre) {
    const [{ n }] = await s<{ n: number }[]>`select count(*)::int + 1 as n from pm_records where ws = ${ws} and kind = ${kind}`;
    code = `${pre}-${String(n).padStart(2, "0")}`;
  }
  const [r] = await s<Rec<T>[]>`insert into pm_records (ws, kind, code, project_id, status, data, due_at)
    values (${ws}, ${kind}, ${code}, ${projectId}, ${opts.status ?? "open"}, ${s.json(JSON.parse(JSON.stringify(data)))}, ${opts.dueAt ?? null})
    returning *`;
  return r;
}

export async function listRecords<T>(kind: RecordKind | RecordKind[], opts: { projectId?: string | null; status?: string | string[]; limit?: number } = {}): Promise<Rec<T>[]> {
  if (!sql) return [];
  const kinds = Array.isArray(kind) ? kind : [kind];
  const statuses = opts.status === undefined ? null : Array.isArray(opts.status) ? opts.status : [opts.status];
  return sql<Rec<T>[]>`select * from pm_records where ws = ${workspace()} and kind = any(${kinds})
    ${opts.projectId ? sql`and (project_id = ${opts.projectId} or project_id is null)` : sql``}
    ${statuses ? sql`and status = any(${statuses})` : sql``}
    order by coalesce(due_at, created_at) desc limit ${opts.limit ?? 100}`;
}

export async function getRecord<T>(idOrCode: number | string): Promise<Rec<T> | null> {
  if (!sql) return null;
  const rows = typeof idOrCode === "number"
    ? await sql<Rec<T>[]>`select * from pm_records where id = ${idOrCode} and ws = ${workspace()}`
    : await sql<Rec<T>[]>`select * from pm_records where upper(code) = ${idOrCode.toUpperCase()} and ws = ${workspace()} order by id desc limit 1`;
  return rows[0] ?? null;
}

export async function updateRecord<T>(id: number, patch: { status?: string; data?: Partial<T>; dueAt?: Date | null }): Promise<Rec<T> | null> {
  const s = db();
  const [r] = await s<Rec<T>[]>`update pm_records set
      status = coalesce(${patch.status ?? null}, status),
      data = data || ${s.json(JSON.parse(JSON.stringify(patch.data ?? {})))},
      due_at = ${patch.dueAt === undefined ? s`due_at` : patch.dueAt},
      updated_at = now()
    where id = ${id} and ws = ${workspace()} returning *`;
  return r ?? null;
}

export async function deleteRecord(id: number) {
  await db()`delete from pm_records where id = ${id} and ws = ${workspace()}`;
}

/** Reminders whose time has come (any workspace: reminders are personal). */
export async function dueReminders(): Promise<Rec<ReminderData>[]> {
  if (!sql) return [];
  return sql<Rec<ReminderData>[]>`select * from pm_records where kind = 'reminder' and status = 'open' and due_at <= now() order by due_at limit 5`;
}

export const LEVEL_TH: Record<Level, string> = { high: "สูง", medium: "กลาง", low: "ต่ำ" };
