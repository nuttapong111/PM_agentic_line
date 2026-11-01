// Pure computations over Plane data: used by the dashboard, the morning
// digest and the project_status tool. Numbers are computed here in code, never
// by the model.
import { config } from "./config.js";
import type { Project, State, Member, WorkItem, Cycle, Module, CycleLink, Label } from "./plane.js";
import type { TaskProvider as PlaneClient } from "./providers/types.js";
import { store } from "./store.js";

const DAY = 86_400_000;

/** YYYY-MM-DD in the configured timezone. */
export function dateStr(d: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: config.timezone, year: "numeric", month: "2-digit", day: "2-digit" }).format(d);
}

export function daysBetween(a: string, b: string): number {
  return Math.round((Date.parse(b.slice(0, 10)) - Date.parse(a.slice(0, 10))) / DAY);
}

const TH_MONTHS = ["ม.ค.", "ก.พ.", "มี.ค.", "เม.ย.", "พ.ค.", "มิ.ย.", "ก.ค.", "ส.ค.", "ก.ย.", "ต.ค.", "พ.ย.", "ธ.ค."];
const TH_DAYS = ["อา.", "จ.", "อ.", "พ.", "พฤ.", "ศ.", "ส."];

/** "ศ. 7 พ.ย." */
export function thaiDate(ymd: string | null, withDay = true): string {
  if (!ymd) return "-";
  const [y, m, d] = ymd.slice(0, 10).split("-").map(Number);
  const dow = new Date(Date.UTC(y, m - 1, d)).getUTCDay();
  return `${withDay ? TH_DAYS[dow] + " " : ""}${d} ${TH_MONTHS[m - 1]}`;
}

export interface ItemView {
  key: string;
  name: string;
  state: string;
  stateGroup: string;
  assignees: string[];
  priority: string;
  target: string | null;
  daysOverdue?: number;
  daysStale?: number;
  url: string;
}

export interface ProjectMetrics {
  project: { id: string; name: string; identifier: string; url: string };
  today: string;
  counts: { open: number; overdue: number; blocked: number; doneThisWeek: number; urgentBugs: number; unassigned: number };
  overdue: ItemView[];
  dueToday: ItemView[];
  dueNext7: ItemView[];
  blocked: ItemView[];
  stale: ItemView[];
  workload: { name: string; open: number; dueThisWeek: number }[];
  bugsByPriority: { priority: string; count: number }[];
  cycle: null | {
    name: string;
    start: string;
    end: string;
    daysLeft: number;
    total: number;
    done: number;
    percent: number;
    initial: number;
    addedMidway: number;
    projectedMiss: number;
    burndown: { date: string; remaining: number; ideal: number }[];
  };
  modules: { name: string; total: number; done: number; percent: number; target: string | null; status: "on_track" | "watch" | "at_risk" | "late" | "done" }[];
}

export interface ProjectContext {
  project: Project;
  items: WorkItem[];
  states: Map<string, State>;
  members: Map<string, Member>;
  labels: Label[];
}

export async function loadContext(plane: PlaneClient, project: Project): Promise<ProjectContext> {
  const [items, states, members, labels] = await Promise.all([
    plane.workItems(project),
    plane.states(project.id),
    plane.members(project.id),
    plane.labels(project.id).catch(() => [] as Label[]),
  ]);
  return {
    project,
    items,
    states: new Map(states.map((s) => [s.id, s])),
    members: new Map(members.map((m) => [m.id, m])),
    labels,
  };
}

export function memberName(m: Member | undefined): string {
  if (!m) return "?";
  return m.display_name || [m.first_name, m.last_name].filter(Boolean).join(" ") || m.email || "?";
}

export function isOpen(ctx: ProjectContext, it: WorkItem): boolean {
  const g = ctx.states.get(it.stateId)?.group;
  return g !== "completed" && g !== "cancelled";
}

export function isBlocked(ctx: ProjectContext, it: WorkItem): boolean {
  return /block/i.test(ctx.states.get(it.stateId)?.name ?? "");
}

export function view(ctx: ProjectContext, it: WorkItem, plane: PlaneClient, today = dateStr()): ItemView {
  const st = ctx.states.get(it.stateId);
  const v: ItemView = {
    key: it.key,
    name: it.name,
    state: st?.name ?? "?",
    stateGroup: st?.group ?? "?",
    assignees: it.assigneeIds.map((id) => memberName(ctx.members.get(id))),
    priority: it.priority,
    target: it.targetDate,
    url: plane.itemUrl(it),
  };
  if (it.targetDate && it.targetDate < today && isOpen(ctx, it)) v.daysOverdue = daysBetween(it.targetDate, today);
  return v;
}

export async function projectMetrics(plane: PlaneClient, project: Project): Promise<ProjectMetrics> {
  const ctx = await loadContext(plane, project);
  const today = dateStr();
  const open = ctx.items.filter((i) => isOpen(ctx, i));
  const v = (i: WorkItem) => view(ctx, i, plane, today);
  const byTarget = (a: WorkItem, b: WorkItem) => (a.targetDate ?? "").localeCompare(b.targetDate ?? "");

  const overdue = open.filter((i) => i.targetDate && i.targetDate < today).sort(byTarget);
  const dueToday = open.filter((i) => i.targetDate === today);
  const in7 = dateStr(new Date(Date.now() + 7 * DAY));
  const dueNext7 = open.filter((i) => i.targetDate && i.targetDate > today && i.targetDate <= in7).sort(byTarget);
  const blocked = open.filter((i) => isBlocked(ctx, i));

  const weekStart = startOfWeek(today);
  const doneThisWeek = ctx.items.filter((i) => i.completedAt && dateStr(new Date(i.completedAt)) >= weekStart).length;

  const staleCut = Date.now() - store.get().prefs.staleDays * DAY;
  const stale = open
    .filter((i) => {
      const g = ctx.states.get(i.stateId)?.group;
      return (g === "started" || isBlocked(ctx, i)) && Date.parse(i.updatedAt) < staleCut;
    })
    .sort((a, b) => a.updatedAt.localeCompare(b.updatedAt))
    .map((i) => ({ ...v(i), daysStale: Math.floor((Date.now() - Date.parse(i.updatedAt)) / DAY) }));

  // workload
  const load = new Map<string, { open: number; dueThisWeek: number }>();
  const weekEnd = dateStr(new Date(Date.parse(weekStart) + 6 * DAY));
  for (const i of open) {
    const names = i.assigneeIds.length ? i.assigneeIds.map((id) => memberName(ctx.members.get(id))) : ["ไม่มีเจ้าของ"];
    for (const n of names) {
      const e = load.get(n) ?? { open: 0, dueThisWeek: 0 };
      e.open++;
      if (i.targetDate && i.targetDate <= weekEnd) e.dueThisWeek++;
      load.set(n, e);
    }
  }
  const workload = [...load.entries()].map(([name, e]) => ({ name, ...e })).sort((a, b) => b.open - a.open);

  // bugs
  const bugLabelIds = new Set(ctx.labels.filter((l) => l.name.toLowerCase() === store.get().prefs.bugLabel.toLowerCase()).map((l) => l.id));
  const bugs = open.filter((i) => i.labelIds.some((l) => bugLabelIds.has(l)));
  const prios = ["urgent", "high", "medium", "low", "none"];
  const bugsByPriority = prios.map((p) => ({ priority: p, count: bugs.filter((b) => b.priority === p).length })).filter((b) => b.count > 0 || b.priority !== "none");

  const cycle = await cycleMetrics(plane, ctx, today);
  const modules = await moduleMetrics(plane, ctx, today);

  return {
    project: { id: project.id, name: project.name, identifier: project.identifier, url: plane.projectUrl(project) },
    today,
    counts: {
      open: open.length,
      overdue: overdue.length,
      blocked: blocked.length,
      doneThisWeek,
      urgentBugs: bugs.filter((b) => b.priority === "urgent" || b.priority === "high").length,
      unassigned: open.filter((i) => i.assigneeIds.length === 0).length,
    },
    overdue: overdue.map(v),
    dueToday: dueToday.map(v),
    dueNext7: dueNext7.map(v),
    blocked: blocked.map(v),
    stale,
    workload,
    bugsByPriority,
    cycle,
    modules,
  };
}

export function startOfWeek(today: string): string {
  const d = new Date(today + "T00:00:00Z");
  const dow = (d.getUTCDay() + 6) % 7; // Monday = 0
  return new Date(d.getTime() - dow * DAY).toISOString().slice(0, 10);
}

export function currentCycle(cycles: Cycle[], today: string): Cycle | undefined {
  return (
    cycles.find((c) => c.status === "current") ??
    cycles.find((c) => c.start_date && c.end_date && c.start_date.slice(0, 10) <= today && c.end_date.slice(0, 10) >= today)
  );
}

export async function cycleMetrics(plane: PlaneClient, ctx: ProjectContext, today: string, forCycle?: Cycle): Promise<ProjectMetrics["cycle"]> {
  const c = forCycle ?? currentCycle(await plane.cycles(ctx.project.id).catch(() => [] as Cycle[]), today);
  if (!c || !c.start_date || !c.end_date) return null;
  const start = c.start_date.slice(0, 10);
  const end = c.end_date.slice(0, 10);
  const links: CycleLink[] = await plane.cycleLinks(ctx.project.id, c.id).catch(() => []);
  const byId = new Map(ctx.items.map((i) => [i.id, i]));
  const rows = links
    .map((l) => ({ item: byId.get(l.issueId), added: dateStr(new Date(l.addedAt)) }))
    .filter((r): r is { item: WorkItem; added: string } => !!r.item && ctx.states.get(r.item.stateId)?.group !== "cancelled");

  const isDone = (i: WorkItem) => ctx.states.get(i.stateId)?.group === "completed";
  const total = rows.length;
  const done = rows.filter((r) => isDone(r.item)).length;
  const initial = rows.filter((r) => r.added <= start).length;
  const addedMidway = total - initial;

  const span = Math.max(1, daysBetween(start, end));
  const lastDay = today < end ? today : end;
  const burndown: { date: string; remaining: number; ideal: number }[] = [];
  for (let d = 0; d <= daysBetween(start, lastDay); d++) {
    const date = dateStr(new Date(Date.parse(start) + d * DAY + 12 * 3600_000));
    const scope = rows.filter((r) => r.added <= date);
    const doneBy = scope.filter((r) => r.item.completedAt && dateStr(new Date(r.item.completedAt)) <= date).length;
    burndown.push({ date, remaining: scope.length - doneBy, ideal: Math.max(0, Math.round(initial * (1 - d / span) * 10) / 10) });
  }
  const elapsed = Math.max(1, daysBetween(start, lastDay));
  const velocity = done / elapsed;
  const daysLeft = Math.max(0, daysBetween(today, end));
  const remaining = total - done;
  const projectedMiss = Math.max(0, Math.round(remaining - velocity * daysLeft));

  return {
    name: c.name, start, end, daysLeft, total, done,
    percent: total ? Math.round((done / total) * 100) : 0,
    initial, addedMidway, projectedMiss, burndown,
  };
}

async function moduleMetrics(plane: PlaneClient, ctx: ProjectContext, today: string): Promise<ProjectMetrics["modules"]> {
  const mods: Module[] = await plane.modules(ctx.project.id).catch(() => []);
  const byId = new Map(ctx.items.map((i) => [i.id, i]));
  const out: ProjectMetrics["modules"] = [];
  for (const m of mods) {
    const ids = await plane.moduleItemIds(ctx.project.id, m.id).catch(() => [] as string[]);
    const items = ids.map((id) => byId.get(id)).filter((i): i is WorkItem => !!i && ctx.states.get(i.stateId)?.group !== "cancelled");
    const total = items.length;
    const done = items.filter((i) => ctx.states.get(i.stateId)?.group === "completed").length;
    const pct = total ? done / total : 0;
    let status: ProjectMetrics["modules"][number]["status"] = "on_track";
    const target = m.target_date?.slice(0, 10) ?? null;
    if (total > 0 && done === total) status = "done";
    else if (target && target < today) status = "late";
    else if (target && m.start_date) {
      const span = Math.max(1, daysBetween(m.start_date, target));
      const expected = Math.min(1, Math.max(0, daysBetween(m.start_date, today) / span));
      if (pct < expected - 0.25) status = "at_risk";
      else if (pct < expected - 0.1) status = "watch";
    }
    out.push({ name: m.name, total, done, percent: Math.round(pct * 100), target, status });
  }
  const rank = { late: 0, at_risk: 1, watch: 2, on_track: 3, done: 4 };
  return out.sort((a, b) => rank[a.status] - rank[b.status] || (a.target ?? "9").localeCompare(b.target ?? "9"));
}
