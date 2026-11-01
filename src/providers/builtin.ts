// Built-in task list stored in Postgres: the fallback when there is no external tool.
// Fixed states, members = nicknames from Settings, no cycles or modules.
import crypto from "node:crypto";
import { config } from "../config.js";
import { sql } from "../db.js";
import { store } from "../store.js";
import type { TaskProvider, Project, State, Member, WorkItem, WorkItemWrite } from "./types.js";

const STATES: State[] = [
  { id: "todo", name: "Todo", group: "unstarted" },
  { id: "in_progress", name: "In Progress", group: "started" },
  { id: "blocked", name: "Blocked", group: "started" },
  { id: "done", name: "Done", group: "completed" },
  { id: "cancelled", name: "Cancelled", group: "cancelled" },
];

const ME: Member = { id: "me", display_name: "ฉัน" };

export async function migrateBuiltin() {
  if (!sql) return;
  await sql`create table if not exists bt_projects (id text primary key, identifier text unique not null, name text not null, created_at timestamptz not null default now())`;
  await sql`create table if not exists bt_items (
    id text primary key, project_id text not null references bt_projects(id) on delete cascade, seq int not null,
    name text not null, description_html text, state text not null default 'todo', assignees text[] not null default '{}',
    priority text not null default 'none', start_date date, target_date date, completed_at timestamptz,
    parent_id text, created_at timestamptz not null default now(), updated_at timestamptz not null default now(),
    unique (project_id, seq))`;
  await sql`create table if not exists bt_comments (id bigserial primary key, item_id text not null references bt_items(id) on delete cascade, html text not null, created_at timestamptz not null default now())`;
}

export async function createBuiltinProject(name: string, identifier: string): Promise<Project> {
  if (!sql) throw new Error("ต้องมีฐานข้อมูล");
  const id = crypto.randomUUID();
  const ident = identifier.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 12);
  if (!ident) throw new Error("รหัสโปรเจกต์ต้องเป็นตัวอักษรภาษาอังกฤษหรือตัวเลข");
  await sql`insert into bt_projects (id, identifier, name) values (${id}, ${ident}, ${name})`;
  return { id, name, identifier: ident };
}

type Row = {
  id: string; project_id: string; seq: number; name: string; state: string; assignees: string[]; priority: string;
  start_date: string | null; target_date: string | null; completed_at: Date | null; created_at: Date; updated_at: Date; parent_id: string | null;
};

export class BuiltinProvider implements TaskProvider {
  readonly kind = "builtin" as const;
  readonly workspaceKey = "builtin";

  private db() {
    if (!sql) throw new Error("ระบบงานในตัวต้องใช้ฐานข้อมูล PostgreSQL");
    return sql;
  }

  private toItem(r: Row, p: Project): WorkItem {
    return {
      id: r.id, projectId: r.project_id, key: `${p.identifier}-${r.seq}`, name: r.name, priority: r.priority, stateId: r.state,
      assigneeIds: r.assignees, labelIds: [], startDate: r.start_date, targetDate: r.target_date,
      completedAt: r.completed_at?.toISOString() ?? null, createdAt: r.created_at.toISOString(), updatedAt: r.updated_at.toISOString(),
      parentId: r.parent_id,
    };
  }

  async me() { return ME; }
  async allProjects(): Promise<Project[]> {
    return this.db()<Project[]>`select id, name, identifier from bt_projects order by created_at`;
  }
  async projects() { return this.allProjects(); }
  async states() { return STATES; }
  async members(): Promise<Member[]> {
    const nick = Object.keys(store.get().prefs.nicknames);
    return [ME, ...nick.map((n) => ({ id: n, display_name: n }))];
  }
  async labels() { return []; }
  async cycles() { return []; }
  async modules() { return []; }
  async cycleLinks() { return []; }
  async moduleItemIds() { return []; }

  async workItems(project: Project): Promise<WorkItem[]> {
    const rows = await this.db()<Row[]>`select *, to_char(start_date,'YYYY-MM-DD') as start_date, to_char(target_date,'YYYY-MM-DD') as target_date from bt_items where project_id = ${project.id} order by seq`;
    return rows.map((r) => this.toItem(r, project));
  }

  async workItemByKey(key: string): Promise<WorkItem> {
    const m = key.toUpperCase().match(/^([A-Z0-9]+)-(\d+)$/);
    if (!m) throw new Error(`รหัสงาน "${key}" ไม่ถูกรูปแบบ`);
    const p = (await this.projects()).find((x) => x.identifier === m[1]);
    if (!p) throw new Error(`ไม่พบโปรเจกต์ ${m[1]}`);
    const [r] = await this.db()<Row[]>`select *, to_char(start_date,'YYYY-MM-DD') as start_date, to_char(target_date,'YYYY-MM-DD') as target_date from bt_items where project_id = ${p.id} and seq = ${Number(m[2])}`;
    if (!r) throw new Error(`ไม่พบงาน ${key}`);
    return this.toItem(r, p);
  }

  async createWorkItem(project: Project, b: WorkItemWrite): Promise<WorkItem> {
    const db = this.db();
    const id = crypto.randomUUID();
    const [r] = await db<Row[]>`insert into bt_items (id, project_id, seq, name, description_html, state, assignees, priority, start_date, target_date, parent_id)
      values (${id}, ${project.id}, (select coalesce(max(seq),0)+1 from bt_items where project_id = ${project.id}), ${b.name ?? "งานใหม่"},
        ${b.description_html ?? null}, ${b.state ?? "todo"}, ${b.assignees ?? []}, ${b.priority ?? "none"}, ${b.start_date ?? null}, ${b.target_date ?? null}, ${b.parent ?? null})
      returning *, to_char(start_date,'YYYY-MM-DD') as start_date, to_char(target_date,'YYYY-MM-DD') as target_date`;
    return this.toItem(r, project);
  }

  async updateWorkItem(project: Project, id: string, b: WorkItemWrite): Promise<WorkItem> {
    const db = this.db();
    const done = b.state === "done";
    const [r] = await db<Row[]>`update bt_items set
        name = coalesce(${b.name ?? null}, name),
        state = coalesce(${b.state ?? null}, state),
        assignees = coalesce(${b.assignees ?? null}::text[], assignees),
        priority = coalesce(${b.priority ?? null}, priority),
        start_date = coalesce(${b.start_date ?? null}::date, start_date),
        target_date = coalesce(${b.target_date ?? null}::date, target_date),
        completed_at = case when ${b.state ?? null}::text is null then completed_at when ${done} then now() else null end,
        updated_at = now()
      where id = ${id} and project_id = ${project.id}
      returning *, to_char(start_date,'YYYY-MM-DD') as start_date, to_char(target_date,'YYYY-MM-DD') as target_date`;
    if (!r) throw new Error("ไม่พบงาน");
    return this.toItem(r, project);
  }

  async addToCycle() { /* no cycles in the built-in store */ }

  async addComment(_p: Project, itemId: string, html: string) {
    await this.db()`insert into bt_comments (item_id, html) values (${itemId}, ${html})`;
    await this.db()`update bt_items set updated_at = now() where id = ${itemId}`;
  }

  itemUrl(item: WorkItem) { return this.keyUrl(item.key); }
  keyUrl(key: string) { return `${config.publicUrl}/dashboard#${encodeURIComponent(key)}`; }
  projectUrl() { return `${config.publicUrl}/dashboard`; }
}
