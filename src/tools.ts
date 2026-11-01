// Tools the agent can call. Read tools run immediately. Write tools are
// "prepared" first (names resolved to IDs, a preview built) and only run after
// the owner taps ยืนยัน on the confirm card.
import type Anthropic from "@anthropic-ai/sdk";
import { config } from "./config.js";
import { PlaneError, type WorkItem, type WorkItemWrite } from "./plane.js";
import { projectMetrics, loadContext, view, isOpen, dateStr, memberName, currentCycle, thaiDate } from "./metrics.js";
import { store } from "./store.js";
import { plane, UserFacingError, resolveProject, projectOfKey, resolveMember, resolveState, checkDate, escapeHtml, nicknames } from "./resolve.js";
import { featureToolDefs, FEATURE_WRITE_TOOLS, featureToolNames, runFeatureRead, prepareFeatureWrite, executeFeature, type FeaturePlan } from "./features.js";

// ---------------------------------------------------------------- schemas

const projectParam = {
  type: "string",
  description: "ชื่อหรือรหัสโปรเจกต์ (เช่น FDABIGDATA หรือ \"FDA Big data\"). เว้นไว้ = โปรเจกต์ปัจจุบัน",
};

export const toolDefs: Anthropic.Tool[] = [
  {
    name: "list_projects",
    description: "รายชื่อโปรเจกต์ในระบบจัดการงานที่ติดตามอยู่ พร้อมบอกว่าอันไหนเป็นโปรเจกต์ปัจจุบัน",
    input_schema: { type: "object", properties: {}, additionalProperties: false },
  },
  {
    name: "set_current_project",
    description: "ตั้งโปรเจกต์ปัจจุบัน ใช้เมื่อผู้ใช้บอกให้สลับโปรเจกต์",
    input_schema: { type: "object", properties: { project: projectParam }, required: ["project"], additionalProperties: false },
  },
  {
    name: "project_status",
    description:
      "ภาพรวมโปรเจกต์: cycle ปัจจุบัน (ความคืบหน้า, คาดว่าหลุดกี่งาน, งานที่เพิ่มกลาง sprint), งานเลยกำหนด, blocked, ค้างนาน, งานต่อคน, module เทียบกำหนดส่ง, bug ตาม priority. ตัวเลขคำนวณจากข้อมูลจริงแล้ว ให้ใช้ตามนั้น",
    input_schema: { type: "object", properties: { project: projectParam }, additionalProperties: false },
  },
  {
    name: "search_work_items",
    description: "ค้นหางานในโปรเจกต์ด้วยเงื่อนไข คืนรายการงานพร้อมรหัส สถานะ ผู้รับผิดชอบ กำหนดส่ง",
    input_schema: {
      type: "object",
      properties: {
        project: projectParam,
        text: { type: "string", description: "คำค้นในชื่องาน" },
        assignee: { type: "string", description: "ชื่อหรือชื่อเล่นผู้รับผิดชอบ, \"me\" = ผู้ใช้เอง, \"none\" = ไม่มีเจ้าของ" },
        status: { type: "string", enum: ["open", "overdue", "due_today", "due_7_days", "blocked", "done", "all"], description: "ค่าเริ่มต้น open" },
        in_current_cycle: { type: "boolean" },
        limit: { type: "integer", minimum: 1, maximum: 30, description: "ค่าเริ่มต้น 10" },
      },
      additionalProperties: false,
    },
  },
  {
    name: "get_work_item",
    description: "รายละเอียดงานจากรหัส เช่น FDABIGDATA-123 (รู้โปรเจกต์จากรหัสเอง)",
    input_schema: { type: "object", properties: { key: { type: "string" } }, required: ["key"], additionalProperties: false },
  },
  {
    name: "list_members_and_states",
    description: "สมาชิกและสถานะ (state) ที่ใช้ได้ในโปรเจกต์ ใช้เมื่อต้องเลือกคนหรือสถานะแต่ไม่แน่ใจชื่อ",
    input_schema: { type: "object", properties: { project: projectParam }, additionalProperties: false },
  },
  {
    name: "create_work_items",
    description:
      "สร้างงานใหม่ 1 ถึง 10 งาน (ต้องให้ผู้ใช้ยืนยันก่อน ระบบจะส่งการ์ดยืนยันเอง). ถ้าผู้ใช้ขอให้แตกงานย่อย ให้ใส่งานแม่เป็นรายการแรกและใส่ parent_index: 0 ในงานย่อย. วันที่ใช้รูปแบบ YYYY-MM-DD",
    input_schema: {
      type: "object",
      properties: {
        project: projectParam,
        add_to_current_cycle: { type: "boolean", description: "ค่าเริ่มต้น true" },
        items: {
          type: "array",
          minItems: 1,
          maxItems: 10,
          items: {
            type: "object",
            properties: {
              name: { type: "string" },
              description: { type: "string" },
              assignee: { type: "string", description: "ชื่อหรือชื่อเล่น เว้นได้" },
              priority: { type: "string", enum: ["urgent", "high", "medium", "low", "none"] },
              state: { type: "string", description: "ชื่อสถานะ เว้นไว้ = ค่าเริ่มต้นของโปรเจกต์" },
              start_date: { type: "string" },
              target_date: { type: "string" },
              parent_index: { type: "integer", minimum: 0 },
              tag: { type: "string", description: "ป้ายสั้นสำหรับการ์ด เช่น BE, FE, QA" },
            },
            required: ["name"],
            additionalProperties: false,
          },
        },
      },
      required: ["items"],
      additionalProperties: false,
    },
  },
  {
    name: "update_work_item",
    description: "แก้งานที่มีอยู่ (ต้องยืนยันก่อน): เปลี่ยนชื่อ สถานะ ผู้รับผิดชอบ priority วันที่ ย้ายเข้า cycle ปัจจุบัน",
    input_schema: {
      type: "object",
      properties: {
        key: { type: "string", description: "รหัสงาน เช่น PEALICENSE-27" },
        name: { type: "string" },
        state: { type: "string", description: "ชื่อสถานะ เช่น In Progress, Done, Blocked" },
        assignee: { type: "string", description: "ชื่อหรือชื่อเล่น; \"none\" = เอาออก" },
        priority: { type: "string", enum: ["urgent", "high", "medium", "low", "none"] },
        start_date: { type: "string" },
        target_date: { type: "string" },
        move_to_current_cycle: { type: "boolean" },
      },
      required: ["key"],
      additionalProperties: false,
    },
  },
  {
    name: "add_comment",
    description: "เพิ่มคอมเมนต์ในงาน (ต้องยืนยันก่อน)",
    input_schema: {
      type: "object",
      properties: { key: { type: "string" }, text: { type: "string" } },
      required: ["key", "text"],
      additionalProperties: false,
    },
  },
  {
    name: "dashboard_link",
    description: "ลิงก์หน้า dashboard บนเว็บ",
    input_schema: { type: "object", properties: { project: projectParam }, additionalProperties: false },
  },
];

toolDefs.push(...featureToolDefs);

export const WRITE_TOOLS = new Set(["create_work_items", "update_work_item", "add_comment", ...FEATURE_WRITE_TOOLS]);

// ---------------------------------------------------------------- helpers

export { UserFacingError, resolveProject, plane };

const compact = (v: ReturnType<typeof view>) => ({
  key: v.key, name: v.name, state: v.state, assignees: v.assignees, priority: v.priority, target: v.target,
  ...(v.daysOverdue ? { days_overdue: v.daysOverdue } : {}),
});

// ---------------------------------------------------------------- read tools

export async function runReadTool(name: string, input: any): Promise<unknown> {
  switch (name) {
    case "list_projects": {
      const cur = store.get().currentProjectId;
      return (await plane.projects()).map((p) => ({ name: p.name, identifier: p.identifier, current: p.id === cur }));
    }
    case "set_current_project": {
      const p = await resolveProject(input.project);
      store.update((s) => (s.currentProjectId = p.id));
      return { ok: true, current_project: p.name };
    }
    case "project_status": {
      const p = await resolveProject(input.project);
      const m = await projectMetrics(plane, p);
      return {
        project: m.project.name,
        today: m.today,
        counts: m.counts,
        cycle: m.cycle && { ...m.cycle, burndown: undefined },
        overdue: m.overdue.slice(0, 5).map(compact),
        blocked: m.blocked.slice(0, 5).map(compact),
        stale: m.stale.slice(0, 5).map((s) => ({ ...compact(s), days_stale: s.daysStale })),
        due_today: m.dueToday.map(compact),
        workload: m.workload,
        modules: m.modules,
        bugs_by_priority: m.bugsByPriority,
      };
    }
    case "search_work_items": {
      const p = await resolveProject(input.project);
      const ctx = await loadContext(plane, p);
      const today = dateStr();
      const in7 = dateStr(new Date(Date.now() + 7 * 86_400_000));
      let items = ctx.items;
      const status = input.status ?? "open";
      const open = (i: WorkItem) => isOpen(ctx, i);
      if (status === "open") items = items.filter(open);
      if (status === "overdue") items = items.filter((i) => open(i) && i.targetDate && i.targetDate < today);
      if (status === "due_today") items = items.filter((i) => open(i) && i.targetDate === today);
      if (status === "due_7_days") items = items.filter((i) => open(i) && i.targetDate && i.targetDate >= today && i.targetDate <= in7);
      if (status === "blocked") items = items.filter((i) => open(i) && /block/i.test(ctx.states.get(i.stateId)?.name ?? ""));
      if (status === "done") items = items.filter((i) => ctx.states.get(i.stateId)?.group === "completed");
      if (input.text) {
        const q = String(input.text).toLowerCase();
        items = items.filter((i) => i.name.toLowerCase().includes(q));
      }
      if (input.assignee) {
        if (input.assignee === "none") items = items.filter((i) => i.assigneeIds.length === 0);
        else {
          const m = await resolveMember(p.id, input.assignee);
          items = items.filter((i) => m && i.assigneeIds.includes(m.id));
        }
      }
      if (input.in_current_cycle) {
        const c = currentCycle(await plane.cycles(p.id), today);
        const ids = new Set(c ? (await plane.cycleLinks(p.id, c.id)).map((l) => l.issueId) : []);
        items = items.filter((i) => ids.has(i.id));
      }
      items = [...items].sort((a, b) => (a.targetDate ?? "9999").localeCompare(b.targetDate ?? "9999"));
      const limit = input.limit ?? 10;
      return { project: p.name, total: items.length, items: items.slice(0, limit).map((i) => compact(view(ctx, i, plane, today))) };
    }
    case "get_work_item": {
      const p = await projectOfKey(input.key);
      const ctx = await loadContext(plane, p);
      const it = await plane.workItemByKey(input.key);
      return { project: p.name, ...compact(view(ctx, it, plane)), start: it.startDate, completed_at: it.completedAt, updated_at: it.updatedAt, url: plane.itemUrl(it) };
    }
    case "list_members_and_states": {
      const p = await resolveProject(input.project);
      const [members, states] = await Promise.all([plane.members(p.id), plane.states(p.id)]);
      return {
        project: p.name,
        members: members.map((m) => memberName(m)),
        nicknames: nicknames(),
        states: states.map((s) => ({ name: s.name, group: s.group })),
      };
    }
    case "dashboard_link": {
      const p = await resolveProject(input.project).catch(() => null);
      return { url: dashboardUrl(p?.id) };
    }
  }
  if (featureToolNames.has(name)) return runFeatureRead(name, input);
  throw new Error(`unknown tool ${name}`);
}

export function dashboardUrl(projectId?: string | null) {
  const qs = new URLSearchParams({ t: config.dashboardToken, ...(projectId ? { project: projectId } : {}) });
  return `${config.publicUrl}/dashboard?${qs}`;
}

// ---------------------------------------------------------------- write tools

/** What the confirm card shows. Everything is already resolved to IDs. */
export interface Preview {
  kicker?: string;
  cancelLabel?: string;
  /** HTML shown on the "ดูฉบับเต็ม" page (emails, reports) */
  fullHtml?: string;
  title: string;
  subtitle: string;
  lines: { tag?: string; text: string; sub: string; warn?: boolean }[];
  warnings: string[];
  confirmLabel: string;
  plan: WritePlan;
}

export type WritePlan =
  | FeaturePlan
  | { kind: "create"; projectId: string; cycleId: string | null; items: (WorkItemWrite & { parentIndex?: number })[] }
  | { kind: "update"; projectId: string; itemId: string; key: string; body: WorkItemWrite; cycleId: string | null }
  | { kind: "comment"; projectId: string; itemId: string; key: string; html: string };

export async function prepareWrite(name: string, input: any): Promise<Preview> {
  if (name === "create_work_items") {
    const p = await resolveProject(input.project);
    const states = await plane.states(p.id);
    const today = dateStr();
    const cycle = input.add_to_current_cycle === false ? undefined : currentCycle(await plane.cycles(p.id), today);
    const warnings: string[] = [];
    const items: (WorkItemWrite & { parentIndex?: number })[] = [];
    const lines: Preview["lines"] = [];
    for (const [idx, it] of (input.items as any[]).entries()) {
      checkDate(it.start_date, "start_date");
      checkDate(it.target_date, "target_date");
      const m = it.assignee ? await resolveMember(p.id, it.assignee) : null;
      const st = it.state ? await resolveState(p.id, it.state) : undefined;
      if (it.parent_index !== undefined && (it.parent_index >= idx)) throw new UserFacingError("parent_index ต้องชี้ไปงานที่อยู่ก่อนหน้า");
      items.push({
        name: it.name,
        description_html: it.description ? `<p>${escapeHtml(it.description)}</p>` : undefined,
        assignees: m ? [m.id] : undefined,
        priority: it.priority,
        state: st?.id,
        start_date: it.start_date,
        target_date: it.target_date,
        parentIndex: it.parent_index,
      });
      const isParent = (input.items as any[]).some((x: any) => x.parent_index === idx);
      lines.push({
        tag: it.tag ?? (isParent ? "งานแม่" : undefined),
        text: it.name,
        sub: [m ? memberName(m) : "ยังไม่มีผู้รับผิดชอบ", it.target_date ? `ครบ ${thaiDate(it.target_date)}` : null].filter(Boolean).join(" · "),
        warn: !m && !isParent,
      });
      if (!m && !isParent) warnings.push(`"${it.name}" ยังไม่มีผู้รับผิดชอบ`);
    }
    const defaultState = states.find((s) => s.group === "unstarted") ?? states[0];
    return {
      title: items.length === 1 ? items[0].name! : `${items.length} งานใหม่`,
      subtitle: [p.name, cycle?.name, defaultState?.name].filter(Boolean).join(" · "),
      lines,
      warnings,
      confirmLabel: `ยืนยัน สร้าง ${items.length} งาน`,
      plan: { kind: "create", projectId: p.id, cycleId: cycle?.id ?? null, items },
    };
  }

  if (name === "update_work_item") {
    const p = await projectOfKey(input.key);
    const it = await plane.workItemByKey(input.key);
    const ctx = await loadContext(plane, p);
    const body: WorkItemWrite = {};
    const lines: Preview["lines"] = [];
    if (input.name) { body.name = input.name; lines.push({ text: "ชื่อ", sub: `${it.name} → ${input.name}` }); }
    if (input.state) {
      const st = await resolveState(p.id, input.state);
      body.state = st.id;
      lines.push({ text: "สถานะ", sub: `${ctx.states.get(it.stateId)?.name ?? "?"} → ${st.name}` });
    }
    if (input.assignee) {
      const m = await resolveMember(p.id, input.assignee);
      body.assignees = m ? [m.id] : [];
      const before = it.assigneeIds.map((id) => memberName(ctx.members.get(id))).join(", ") || "ไม่มี";
      lines.push({ text: "ผู้รับผิดชอบ", sub: `${before} → ${m ? memberName(m) : "ไม่มี"}` });
    }
    if (input.priority) { body.priority = input.priority; lines.push({ text: "Priority", sub: `${it.priority} → ${input.priority}` }); }
    if (input.start_date) { checkDate(input.start_date, "start_date"); body.start_date = input.start_date; lines.push({ text: "วันเริ่ม", sub: `${thaiDate(it.startDate)} → ${thaiDate(input.start_date)}` }); }
    if (input.target_date) { checkDate(input.target_date, "target_date"); body.target_date = input.target_date; lines.push({ text: "กำหนดส่ง", sub: `${thaiDate(it.targetDate)} → ${thaiDate(input.target_date)}` }); }
    let cycleId: string | null = null;
    if (input.move_to_current_cycle) {
      const c = currentCycle(await plane.cycles(p.id), dateStr());
      if (!c) throw new UserFacingError("โปรเจกต์นี้ไม่มี cycle ที่กำลังเดินอยู่");
      cycleId = c.id;
      lines.push({ text: "Cycle", sub: `ย้ายเข้า ${c.name}` });
    }
    if (lines.length === 0) throw new UserFacingError("ไม่ได้ระบุว่าจะแก้อะไร");
    return {
      title: `${it.key} ${it.name}`,
      subtitle: `${p.name} · แก้ไข ${lines.length} รายการ`,
      lines,
      warnings: [],
      confirmLabel: "ยืนยัน แก้ไข",
      plan: { kind: "update", projectId: p.id, itemId: it.id, key: it.key, body, cycleId },
    };
  }

  if (name === "add_comment") {
    const p = await projectOfKey(input.key);
    const it = await plane.workItemByKey(input.key);
    return {
      title: `คอมเมนต์ใน ${it.key}`,
      subtitle: it.name,
      lines: [{ text: "ข้อความ", sub: String(input.text).slice(0, 300) }],
      warnings: [],
      confirmLabel: "ยืนยัน ส่งคอมเมนต์",
      plan: { kind: "comment", projectId: p.id, itemId: it.id, key: it.key, html: `<p>${escapeHtml(input.text)}</p>` },
    };
  }
  if (FEATURE_WRITE_TOOLS.has(name)) return prepareFeatureWrite(name, input);
  throw new Error(`unknown write tool ${name}`);
}

export async function executeWrite(plan: WritePlan): Promise<unknown> {
  if (plan.kind !== "create" && plan.kind !== "update" && plan.kind !== "comment") return executeFeature(plan);
  const project = (await plane.projects()).find((p) => p.id === plan.projectId);
  if (!project) throw new PlaneError(404, "project not found");
  if (plan.kind === "create") {
    const created: WorkItem[] = [];
    for (const it of plan.items) {
      const { parentIndex, ...body } = it;
      if (parentIndex !== undefined) body.parent = created[parentIndex]?.id;
      created.push(await plane.createWorkItem(project, body));
    }
    if (plan.cycleId) await plane.addToCycle(project.id, plan.cycleId, created.map((c) => c.id));
    return { created: created.map((c) => ({ key: c.key, name: c.name, url: plane.itemUrl(c) })) };
  }
  if (plan.kind === "update") {
    const it = await plane.updateWorkItem(project, plan.itemId, plan.body);
    if (plan.cycleId) await plane.addToCycle(project.id, plan.cycleId, [plan.itemId]);
    return { updated: { key: plan.key, name: it.name, url: plane.itemUrl(it) } };
  }
  await plane.addComment(project, plan.itemId, plan.html);
  return { commented: plan.key };
}
