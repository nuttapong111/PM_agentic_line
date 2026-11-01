// PostgreSQL (optional). When DATABASE_URL is set the bot keeps its state, an activity log,
// token usage and a daily snapshot of every project's metrics for the history views.
import postgres from "postgres";
import type { ProjectMetrics } from "./metrics.js";

const url = process.env.DATABASE_URL;
export const sql = url ? postgres(url, { max: 3, idle_timeout: 30, onnotice: () => undefined }) : null;

export async function migrate() {
  if (!sql) return;
  await sql`create table if not exists app_state (key text primary key, value jsonb not null, updated_at timestamptz not null default now())`;
  await sql`create table if not exists activity_log (
    id bigserial primary key, at timestamptz not null default now(),
    kind text not null, project_id text, summary text not null, detail jsonb)`;
  await sql`create index if not exists activity_log_at on activity_log (at desc)`;
  await sql`create table if not exists usage_log (
    id bigserial primary key, at timestamptz not null default now(), model text not null,
    input_tokens int not null, output_tokens int not null, cache_read_tokens int not null)`;
  await sql`create table if not exists metrics_daily (
    day date not null, project_id text not null, project_name text not null,
    open int not null, overdue int not null, blocked int not null, stale int not null,
    done_this_week int not null, unassigned int not null, urgent_bugs int not null,
    cycle_name text, cycle_percent int, cycle_total int, cycle_done int,
    metrics jsonb not null, primary key (day, project_id))`;
}

export async function loadState<T>(): Promise<T | null> {
  if (!sql) return null;
  const rows = await sql<{ value: T }[]>`select value from app_state where key = 'state'`;
  return rows[0]?.value ?? null;
}

let saving: Promise<unknown> = Promise.resolve();
let latest: unknown = null;
let queued = false;
/** Coalesces rapid saves: at most one write in flight, always writing the newest state. */
export function saveState(value: unknown) {
  if (!sql) return;
  latest = value;
  if (queued) return;
  queued = true;
  saving = saving.then(async () => {
    queued = false;
    const v = JSON.parse(JSON.stringify(latest));
    await sql`insert into app_state (key, value) values ('state', ${sql.json(v)})
      on conflict (key) do update set value = excluded.value, updated_at = now()`;
  }).catch((e) => console.error("db: save state failed", e?.message ?? e));
}

export function logActivity(kind: string, summary: string, projectId: string | null, detail?: unknown) {
  if (!sql) return;
  sql`insert into activity_log (kind, project_id, summary, detail) values (${kind}, ${projectId}, ${summary}, ${detail === undefined ? null : sql.json(JSON.parse(JSON.stringify(detail)))})`
    .catch((e) => console.error("db: activity log failed", e?.message ?? e));
}

export function logUsage(model: string, input: number, output: number, cacheRead: number) {
  if (!sql) return;
  sql`insert into usage_log (model, input_tokens, output_tokens, cache_read_tokens) values (${model}, ${input}, ${output}, ${cacheRead})`
    .catch((e) => console.error("db: usage log failed", e?.message ?? e));
}

export async function snapshotMetrics(m: ProjectMetrics) {
  if (!sql) return;
  const c = m.cycle;
  await sql`insert into metrics_daily (day, project_id, project_name, open, overdue, blocked, stale, done_this_week, unassigned, urgent_bugs,
      cycle_name, cycle_percent, cycle_total, cycle_done, metrics)
    values (${m.today}, ${m.project.id}, ${m.project.name}, ${m.counts.open}, ${m.counts.overdue}, ${m.counts.blocked}, ${m.stale.length},
      ${m.counts.doneThisWeek}, ${m.counts.unassigned}, ${m.counts.urgentBugs},
      ${c?.name ?? null}, ${c?.percent ?? null}, ${c?.total ?? null}, ${c?.done ?? null}, ${sql.json(JSON.parse(JSON.stringify(m)))})
    on conflict (day, project_id) do update set
      project_name = excluded.project_name, open = excluded.open, overdue = excluded.overdue, blocked = excluded.blocked,
      stale = excluded.stale, done_this_week = excluded.done_this_week, unassigned = excluded.unassigned,
      urgent_bugs = excluded.urgent_bugs, cycle_name = excluded.cycle_name, cycle_percent = excluded.cycle_percent,
      cycle_total = excluded.cycle_total, cycle_done = excluded.cycle_done, metrics = excluded.metrics`;
}

export interface HistoryPoint {
  day: string; open: number; overdue: number; blocked: number; stale: number;
  doneThisWeek: number; unassigned: number; urgentBugs: number; cycleName: string | null; cyclePercent: number | null;
}

export async function history(projectId: string, days: number) {
  if (!sql) return { enabled: false, points: [] as HistoryPoint[], activity: [] as unknown[], usage: [] as unknown[] };
  const points = await sql<HistoryPoint[]>`
    select to_char(day, 'YYYY-MM-DD') as day, open, overdue, blocked, stale, done_this_week as "doneThisWeek",
      unassigned, urgent_bugs as "urgentBugs", cycle_name as "cycleName", cycle_percent as "cyclePercent"
    from metrics_daily where project_id = ${projectId} and day >= current_date - ${days}::int order by day`;
  const activity = await sql`
    select at, kind, summary from activity_log where project_id = ${projectId} or project_id is null
    order by at desc limit 30`;
  const usage = await sql`
    select to_char(date_trunc('month', at), 'YYYY-MM') as month, count(*)::int as calls,
      sum(input_tokens)::int as input, sum(output_tokens)::int as output, sum(cache_read_tokens)::int as "cacheRead"
    from usage_log group by 1 order by 1 desc limit 6`;
  return { enabled: true, points, activity, usage };
}
