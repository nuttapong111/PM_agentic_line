// Computed views built on top of task data + the bot's own records: milestone/TOR status,
// man-day budget, team capacity, weekly report, retro, meeting prep. Numbers come from code.
import { plane, norm, escapeHtml } from "./resolve.js";
import { projectMetrics, loadContext, isOpen, memberName, dateStr, thaiDate, daysBetween, startOfWeek, cycleMetrics, type ProjectMetrics } from "./metrics.js";
import { listRecords, LEVEL_TH, type Rec, type MilestoneData, type BudgetData, type EffortData, type RiskData, type CrData, type DecisionData } from "./records.js";
import { metricsAround } from "./db.js";
import { store } from "./store.js";
import { googleConnected, leaveDays, listEvents, type CalEvent } from "./google.js";
import { notionConnected, searchPages, pageText } from "./notion.js";
import { localToDate } from "./time.js";
import type { Project, WorkItem, Cycle } from "./plane.js";

const DAY = 86_400_000;
const addDays = (ymd: string, n: number) => dateStr(new Date(Date.parse(ymd) + n * DAY + 12 * 3600_000));

// ------------------------------------------------------------------ milestones / TOR

export interface MilestoneStatus {
  id: number; code: string | null; name: string; due: string; daysLeft: number; valuePercent?: number;
  modulePercent: number | null; modules: { name: string; percent: number; status: string }[];
  deliverables: { tor?: string; name: string; status: "done" | "open" | "missing"; note: string; key?: string }[];
  done: number; total: number; status: string;
}

export async function milestoneStatus(project: Project, rec: Rec<MilestoneData>, m?: ProjectMetrics, items?: WorkItem[]): Promise<MilestoneStatus> {
  m ??= await projectMetrics(plane, project);
  const ctx = items ? null : await loadContext(plane, project);
  const all = items ?? ctx!.items;
  const states = ctx?.states ?? new Map((await plane.states(project.id)).map((s) => [s.id, s]));
  const d = rec.data;
  const mods = (d.modules ?? []).map((name) => m!.modules.find((x) => norm(x.name) === norm(name) || norm(x.name).includes(norm(name)))).filter(Boolean) as ProjectMetrics["modules"];
  const tot = mods.reduce((a, x) => a + x.total, 0);
  const dn = mods.reduce((a, x) => a + x.done, 0);
  const deliverables = d.deliverables.map((del) => {
    const it = del.itemKey ? all.find((i) => i.key.toUpperCase() === del.itemKey!.toUpperCase()) : all.find((i) => norm(i.name).includes(norm(del.name)) || norm(del.name).includes(norm(i.name)));
    if (!it) return { tor: del.tor, name: del.name, status: "missing" as const, note: "ยังไม่มีงานใน Plane" };
    const st = states.get(it.stateId);
    if (st?.group === "completed") return { tor: del.tor, name: del.name, status: "done" as const, note: `${it.key} เสร็จแล้ว`, key: it.key };
    return { tor: del.tor, name: del.name, status: "open" as const, note: `${it.key} ${st?.name ?? ""}`.trim(), key: it.key };
  });
  return {
    id: rec.id, code: rec.code, name: d.name, due: d.due, daysLeft: daysBetween(dateStr(), d.due), valuePercent: d.valuePercent,
    modulePercent: tot ? Math.round((dn / tot) * 100) : null,
    modules: mods.map((x) => ({ name: x.name, percent: x.percent, status: x.status })),
    deliverables, done: deliverables.filter((x) => x.status === "done").length, total: deliverables.length, status: rec.status,
  };
}

export async function milestonesFor(project: Project, includeDone = false) {
  const recs = await listRecords<MilestoneData>("milestone", { projectId: project.id, status: includeDone ? undefined : "open" });
  if (!recs.length) return [];
  const [m, ctx] = await Promise.all([projectMetrics(plane, project), loadContext(plane, project)]);
  const out = [];
  for (const r of recs) out.push(await milestoneStatus(project, r, m, ctx.items));
  return out.sort((a, b) => a.due.localeCompare(b.due));
}

// ------------------------------------------------------------------ man-day budget

export async function budgetStatus(project: Project) {
  const [budget] = await listRecords<BudgetData>("budget", { projectId: project.id, limit: 1 });
  const efforts = await listRecords<EffortData>("effort", { projectId: project.id, limit: 5000 });
  const hours = store.get().prefs.manDayHours || 8;
  const ctx = await loadContext(plane, project);
  const live = ctx.items.filter((i) => ctx.states.get(i.stateId)?.group !== "cancelled");
  const done = live.filter((i) => ctx.states.get(i.stateId)?.group === "completed").length;
  const progress = live.length ? done / live.length : 0;
  const used = efforts.reduce((a, e) => a + e.data.hours, 0) / hours;

  const mods = await plane.modules(project.id).catch(() => []);
  const keyToModule = new Map<string, string>();
  for (const mod of mods) {
    const ids = new Set(await plane.moduleItemIds(project.id, mod.id).catch(() => [] as string[]));
    for (const i of ctx.items) if (ids.has(i.id)) keyToModule.set(i.key.toUpperCase(), mod.name);
  }
  const byModule: Record<string, number> = {};
  for (const e of efforts) {
    const mod = (e.data.itemKey && keyToModule.get(e.data.itemKey.toUpperCase())) || "อื่น ๆ";
    byModule[mod] = (byModule[mod] ?? 0) + e.data.hours / hours;
  }
  const total = budget?.data.totalManDays ?? null;
  const usedPct = total ? used / total : null;
  const projected = progress > 0.05 ? used / progress : null;
  const round1 = (x: number) => Math.round(x * 10) / 10;
  const moduleNames = new Set([...Object.keys(budget?.data.modules ?? {}), ...Object.keys(byModule)]);
  return {
    project: project.name, hasBudget: !!budget, totalManDays: total, usedManDays: round1(used), usedPercent: usedPct === null ? null : Math.round(usedPct * 100),
    progressPercent: Math.round(progress * 100), entries: efforts.length,
    burnAheadPercent: usedPct === null ? null : Math.round((usedPct - progress) * 100),
    projectedTotal: projected === null ? null : round1(projected),
    projectedOverrun: projected !== null && total ? round1(projected - total) : null,
    modules: [...moduleNames].map((name) => ({ name, used: round1(byModule[name] ?? 0), budget: budget?.data.modules?.[name] ?? null })),
    basis: `คิดจาก effort ที่บันทึกผ่านบอท (${hours} ชม. = 1 man-day) และความคืบหน้าจากจำนวนงานที่เสร็จ`,
  };
}

// ------------------------------------------------------------------ capacity

export async function capacity(project: Project, weekStartIn?: string) {
  const today = dateStr();
  const ws = weekStartIn ?? addDays(startOfWeek(today), 7);
  const we = addDays(ws, 4);
  const ctx = await loadContext(plane, project);
  const nick = store.get().prefs.nicknames;
  const people = [...ctx.members.values()].map((m) => {
    const aliases = [m.display_name, m.first_name, m.email, ...Object.entries(nick).filter(([, v]) => norm(v) === norm(m.email ?? "") || norm(v) === norm(m.display_name)).map(([k]) => k)].filter(Boolean) as string[];
    return { id: m.id, name: memberName(m), aliases };
  });
  const calId = store.get().prefs.leaveCalendarId;
  let leave: Record<string, string[]> = {};
  let leaveSource = "ไม่ได้ดึงวันลา (ยังไม่ได้ตั้งปฏิทินวันลาในหน้าตั้งค่า)";
  if (calId && googleConnected()) {
    try {
      leave = await leaveDays(calId, ws, we, people);
      leaveSource = "ดึงวันลาจาก Google Calendar";
    } catch (e) {
      leaveSource = `ดึงวันลาไม่ได้: ${e instanceof Error ? e.message : e}`;
    }
  }
  const rows = people.map((p) => {
    const from = ws < today ? today : ws;
    const mine = ctx.items.filter((i) => isOpen(ctx, i) && i.assigneeIds.includes(p.id) && i.targetDate);
    const due = mine.filter((i) => i.targetDate! >= from && i.targetDate! <= we);
    const overdue = mine.filter((i) => i.targetDate! < from);
    const l = (leave[p.name] ?? []).filter((d) => { const w = new Date(d + "T00:00:00Z").getUTCDay(); return w > 0 && w < 6; });
    const days = Math.max(0, 5 - l.length);
    const demand = due.length + overdue.length;
    return {
      name: p.name, leave: l, workDays: days, dueItems: due.length, carriedOverdue: overdue.length,
      loadPercent: days ? Math.round((demand / days) * 100) : demand ? 999 : 0,
      items: [...overdue, ...due].map((i) => ({ key: i.key, name: i.name, target: i.targetDate })),
    };
  }).filter((r) => r.dueItems + r.carriedOverdue > 0 || r.leave.length).sort((a, b) => b.loadPercent - a.loadPercent);
  return {
    project: project.name, week: `${ws} ถึง ${we}`, weekLabel: `${thaiDate(ws, false)} – ${thaiDate(we, false)}`, leaveSource,
    basis: "นับงานที่ครบกำหนดในสัปดาห์นั้น (รวมงานค้างเลยกำหนด) เป็นงานละ 1 วัน เทียบกับวันทำงานหลังหักวันลา",
    overloaded: rows.filter((r) => r.loadPercent > 100).map((r) => r.name), people: rows,
  };
}

// ------------------------------------------------------------------ weekly report

export interface WeeklyReport {
  subject: string; html: string; week: number; rangeLabel: string;
  projects: { name: string; percent: number | null; delta: number | null; overdue: number; blocked: number; note: string }[];
  done: { key: string; name: string }[]; next: string[]; risks: string[];
}

function isoWeek(ymd: string): number {
  const d = new Date(ymd + "T00:00:00Z");
  const day = (d.getUTCDay() + 6) % 7;
  d.setUTCDate(d.getUTCDate() - day + 3);
  const first = new Date(Date.UTC(d.getUTCFullYear(), 0, 4));
  return 1 + Math.round(((d.getTime() - first.getTime()) / DAY - 3 + ((first.getUTCDay() + 6) % 7)) / 7);
}

export async function weeklyReport(): Promise<WeeklyReport> {
  const today = dateStr();
  const ws = startOfWeek(today);
  const nextWs = addDays(ws, 7), nextWe = addDays(ws, 13);
  const weekAgo = Date.now() - 7 * DAY;
  const projects: WeeklyReport["projects"] = [];
  const done: WeeklyReport["done"] = [];
  const next: string[] = [];
  const risks: string[] = [];
  for (const p of await plane.projects()) {
    try {
      const [m, ctx] = await Promise.all([projectMetrics(plane, p), loadContext(plane, p)]);
      const before = await metricsAround(p.id, 7).catch(() => null);
      const pct = m.cycle?.percent ?? null;
      const delta = pct !== null && before?.cyclePercent != null && before.cycleName === m.cycle?.name ? pct - before.cyclePercent : null;
      const notes = [m.counts.overdue ? `เลยกำหนด ${m.counts.overdue} งาน` : null, m.counts.blocked ? `Blocked ${m.counts.blocked} งาน` : null].filter(Boolean);
      projects.push({ name: p.name, percent: pct, delta, overdue: m.counts.overdue, blocked: m.counts.blocked, note: notes.join(", ") || "ตามแผน" });
      for (const i of ctx.items) if (i.completedAt && Date.parse(i.completedAt) >= weekAgo) done.push({ key: i.key, name: i.name });
      for (const mod of m.modules) if (mod.target && mod.target >= nextWs && mod.target <= nextWe && mod.status !== "done") next.push(`ส่งมอบ module ${mod.name} · ${p.name} (${thaiDate(mod.target)})`);
      for (const ms of await listRecords<MilestoneData>("milestone", { projectId: p.id, status: "open" })) {
        if (ms.data.due >= nextWs && ms.data.due <= nextWe) next.push(`${ms.data.name} · ${p.name} (${thaiDate(ms.data.due)})`);
      }
      for (const r of await listRecords<RiskData>("risk", { projectId: p.id, status: "open" })) {
        if (r.data.impact === "high") risks.push(`${r.code} ${r.data.title}${r.data.mitigation ? ` · แผนรองรับ: ${r.data.mitigation}` : ""}`);
      }
    } catch (e) {
      console.error(`weekly report: ${p.identifier} failed`, e instanceof Error ? e.message : e);
    }
  }
  if (googleConnected()) {
    try {
      for (const e of await listEvents(localToDate(nextWs, "00:00"), localToDate(nextWe, "23:59"))) {
        if (!e.allDay && e.attendees.length > 1) next.push(`${e.summary} (${thaiDate(e.start.slice(0, 10))})`);
      }
    } catch { /* calendar optional */ }
  }
  const week = isoWeek(today);
  const rangeLabel = `${thaiDate(ws, false)} – ${thaiDate(addDays(ws, 6), false)}`;
  const subject = `Weekly status · สัปดาห์ที่ ${week} (${rangeLabel})`;
  const li = (xs: string[]) => (xs.length ? `<ul>${xs.map((x) => `<li>${escapeHtml(x)}</li>`).join("")}</ul>` : "<p>-</p>");
  const html = `<div style="font-family:Sarabun,Tahoma,sans-serif;font-size:14px;color:#18202B">
<h2 style="margin:0 0 4px">${escapeHtml(subject)}</h2>
<h3>สรุปสัปดาห์</h3>${li(projects.map((p) => `${p.name}${p.percent !== null ? ` เสร็จ ${p.percent}%${p.delta !== null ? ` (${p.delta >= 0 ? "+" : ""}${p.delta}%)` : ""}` : ""} · ${p.note}`))}
<h3>เสร็จสัปดาห์นี้ ${done.length} งาน</h3>${li(done.slice(0, 30).map((d) => `${d.key} ${d.name}`))}
<h3>สัปดาห์หน้า</h3>${li(next)}
<h3>ความเสี่ยงที่ต้องการความช่วยเหลือ</h3>${li(risks)}
<p style="color:#5A6573;font-size:12px">ร่างจากข้อมูลในระบบจัดการงานโดย PM Assistant</p></div>`;
  return { subject, html, week, rangeLabel, projects, done, next, risks };
}

// ------------------------------------------------------------------ retro

export async function lastEndedCycle(project: Project): Promise<Cycle | null> {
  const today = dateStr();
  const cycles = await plane.cycles(project.id).catch(() => [] as Cycle[]);
  return cycles.filter((c) => c.end_date && c.end_date.slice(0, 10) <= today && c.start_date)
    .sort((a, b) => b.end_date!.localeCompare(a.end_date!))[0] ?? null;
}

export async function retro(project: Project, cycle?: Cycle | null) {
  const today = dateStr();
  const c = cycle ?? (await lastEndedCycle(project));
  if (!c) return null;
  const ctx = await loadContext(plane, project);
  const cm = await cycleMetrics(plane, ctx, today, c);
  if (!cm) return null;
  const links = await plane.cycleLinks(project.id, c.id).catch(() => []);
  const ids = new Set(links.map((l) => l.issueId));
  const inCycle = ctx.items.filter((i) => ids.has(i.id) && ctx.states.get(i.stateId)?.group !== "cancelled");
  const missed = inCycle.filter((i) => ctx.states.get(i.stateId)?.group !== "completed");
  const blockedMissed = missed.filter((i) => /block/i.test(ctx.states.get(i.stateId)?.name ?? ""));
  const late = inCycle.filter((i) => i.completedAt && i.targetDate && dateStr(new Date(i.completedAt)) > i.targetDate);
  const scopePct = cm.initial ? Math.round((cm.addedMidway / cm.initial) * 100) : 0;
  const good: string[] = [], improve: string[] = [], topics: string[] = [];
  if (cm.percent >= 80) good.push(`เสร็จ ${cm.percent}% ของ sprint`);
  if (cm.done) good.push(`ปิดงานได้ ${cm.done} งาน`);
  if (late.length === 0 && cm.done) good.push("งานที่เสร็จส่งทันกำหนดทั้งหมด");
  if (missed.length) improve.push(`${missed.length} งานหลุด sprint${blockedMissed.length ? `, ${blockedMissed.length} ใน ${missed.length} ติด Blocked` : ""}`);
  if (late.length) improve.push(`${late.length} งานเสร็จช้ากว่ากำหนด`);
  if (scopePct >= 15) improve.push(`scope เพิ่มกลาง sprint ${cm.addedMidway} งาน (${scopePct}%)`);
  if (blockedMissed.length) topics.push("จะจัดการงานที่ติดปัจจัยภายนอกยังไง เช่น แยกออกจาก sprint หรือมีแผนสำรอง");
  if (scopePct >= 15) topics.push("จะกันงานที่เข้ามากลาง sprint ยังไง");
  if (late.length) topics.push("ประเมินเวลางานให้แม่นขึ้นได้ยังไง");
  if (!topics.length) topics.push("อะไรที่ทำให้ sprint นี้ไปได้ดี และจะทำต่อยังไง");
  const markdown = [
    `# Retro · ${c.name}`,
    `${project.name} · ${thaiDate(cm.start, false)} – ${thaiDate(cm.end, false)}`,
    `## ผลลัพธ์`,
    `- เสร็จ ${cm.done} / ${cm.total} งาน (${cm.percent}%)`,
    `- Scope เพิ่มกลาง sprint +${cm.addedMidway} งาน (${scopePct}%)`,
    `## ที่ไปได้ดี`, ...good.map((x) => `- ${x}`),
    `## ที่ต้องปรับ`, ...improve.map((x) => `- ${x}`),
    `## หัวข้อชวนคุย`, ...topics.map((x) => `- ${x}`),
    ...(missed.length ? [`## งานที่หลุด`, ...missed.slice(0, 20).map((i) => `- ${i.key} ${i.name}`)] : []),
  ].join("\n");
  return { cycleId: c.id, cycle: c.name, project: project.name, start: cm.start, end: cm.end, total: cm.total, done: cm.done, percent: cm.percent, addedMidway: cm.addedMidway, scopePercent: scopePct, good, improve, topics, missed: missed.map((i) => i.key), markdown };
}

// ------------------------------------------------------------------ meeting prep

export async function projectForText(text: string): Promise<Project | null> {
  const ps = await plane.projects();
  const t = norm(text);
  return ps.find((p) => t.includes(norm(p.identifier)) || t.includes(norm(p.name))) ??
    ps.find((p) => t.includes(norm(p.name.split(/\s+/)[0]))) ?? ps.find((p) => p.id === store.get().currentProjectId) ?? null;
}

export async function meetingPrep(ev: CalEvent) {
  const project = await projectForText(ev.summary);
  const out: {
    event: CalEvent; project: string | null; progress: string | null; openActions: string[]; doneActions: string[];
    risks: string[]; crs: string[]; decisions: string[]; notionUrl?: string;
  } = { event: ev, project: project?.name ?? null, progress: null, openActions: [], doneActions: [], risks: [], crs: [], decisions: [] };
  if (project) {
    const m = await projectMetrics(plane, project);
    const c = m.cycle;
    out.progress = c ? `${c.name} เสร็จ ${c.percent}% · เลยกำหนด ${m.counts.overdue} งาน${m.counts.blocked ? ` · Blocked ${m.counts.blocked}` : ""}` : `งานเปิด ${m.counts.open} · เลยกำหนด ${m.counts.overdue}`;
    const [risks, crs, decisions] = await Promise.all([
      listRecords<RiskData>("risk", { projectId: project.id, status: "open" }),
      listRecords<CrData>("cr", { projectId: project.id, status: "pending" }),
      listRecords<DecisionData>("decision", { projectId: project.id, limit: 3 }),
    ]);
    out.risks = risks.filter((r) => r.data.impact !== "low").slice(0, 3).map((r) => `${r.code} ${r.data.title} (ผลกระทบ${LEVEL_TH[r.data.impact]})`);
    out.crs = crs.slice(0, 3).map((r) => `${r.code} ${r.data.title} · รออนุมัติ`);
    out.decisions = decisions.map((r) => `${r.code} ${r.data.title}`);
  }
  if (notionConnected()) {
    try {
      const pages = (await searchPages(ev.summary, 5)).filter((p) => Date.parse(p.editedAt) < Date.parse(ev.start));
      if (pages[0]) {
        const pg = await pageText(pages[0].id, 4000);
        out.notionUrl = pg.url;
        for (const l of pg.text.split("\n")) {
          const m = l.match(/^\[( |x)\]\s+(.*)/);
          if (m) (m[1] === "x" ? out.doneActions : out.openActions).push(m[2]);
        }
      }
    } catch (e) {
      console.error("meeting prep notion failed", e instanceof Error ? e.message : e);
    }
  }
  return out;
}
