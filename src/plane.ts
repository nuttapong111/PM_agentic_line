// Thin client for the Plane REST API (v1), self-hosted or cloud.
// Docs: https://developers.plane.so/api-reference/introduction
import { config } from "./config.js";

export type StateGroup = "backlog" | "unstarted" | "started" | "completed" | "cancelled";

export interface Project { id: string; name: string; identifier: string }
export interface State { id: string; name: string; group: StateGroup }
export interface Member { id: string; display_name: string; first_name?: string; last_name?: string; email?: string }
export interface Label { id: string; name: string }
export interface Cycle { id: string; name: string; start_date: string | null; end_date: string | null; status?: string }
export interface Module { id: string; name: string; start_date: string | null; target_date: string | null; status?: string }

/** A work item, normalised so the rest of the app never sees raw shapes. */
export interface WorkItem {
  id: string;
  projectId: string;
  key: string; // e.g. FDABIGDATA-123
  name: string;
  priority: string;
  stateId: string;
  assigneeIds: string[];
  labelIds: string[];
  startDate: string | null;
  targetDate: string | null;
  completedAt: string | null;
  createdAt: string;
  updatedAt: string;
  parentId: string | null;
}

export interface CycleLink { issueId: string; addedAt: string }

interface Page<T> { results: T[]; next_cursor?: string; next_page_results?: boolean }

export class PlaneError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

export class PlaneClient {
  private base: string;
  /** "work-items" on recent Plane, "issues" on older self-hosted builds. Detected on first use. */
  private itemsPath: string | null = null;
  private cache = new Map<string, { at: number; value: unknown }>();

  constructor(
    private baseUrl = config.plane.baseUrl,
    private apiKey = config.plane.apiKey,
    private workspace = config.plane.workspace,
    private fetchImpl: typeof fetch = fetch,
  ) {
    this.base = `${this.baseUrl}/api/v1/workspaces/${this.workspace}`;
  }

  // ---------- low level ----------

  private async request<T>(method: string, url: string, body?: unknown, attempt = 0): Promise<T> {
    const res = await this.fetchImpl(url, {
      method,
      headers: { "X-API-Key": this.apiKey, "Content-Type": "application/json", Accept: "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 429 && attempt < 3) {
      // Plane allows 60 requests/minute. Wait until the reset time (or 5s) and retry.
      const reset = Number(res.headers.get("X-RateLimit-Reset"));
      const waitMs = reset ? Math.max(1000, reset * 1000 - Date.now()) : 5000;
      await new Promise((r) => setTimeout(r, Math.min(waitMs, 30000)));
      return this.request<T>(method, url, body, attempt + 1);
    }
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new PlaneError(res.status, `Plane ${method} ${url.replace(this.baseUrl, "")} → ${res.status} ${text.slice(0, 300)}`);
    }
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  /** Follows cursor pagination. Accepts endpoints that return a plain array too. */
  private async listAll<T>(path: string, params: Record<string, string> = {}): Promise<T[]> {
    const out: T[] = [];
    let cursor: string | undefined;
    for (let i = 0; i < 50; i++) {
      const qs = new URLSearchParams({ per_page: "100", ...params, ...(cursor ? { cursor } : {}) });
      const page = await this.request<Page<T> | T[]>("GET", `${this.base}${path}?${qs}`);
      if (Array.isArray(page)) {
        // Some endpoints (project-members) return a bare array, sometimes nested.
        const flat = (page as unknown[]).flat() as T[];
        out.push(...flat);
        return out;
      }
      out.push(...page.results);
      if (!page.next_page_results || !page.next_cursor) break;
      cursor = page.next_cursor;
    }
    return out;
  }

  private async cached<T>(key: string, ttlMs: number, load: () => Promise<T>): Promise<T> {
    const hit = this.cache.get(key);
    if (hit && Date.now() - hit.at < ttlMs) return hit.value as T;
    const value = await load();
    this.cache.set(key, { at: Date.now(), value });
    return value;
  }

  invalidate(prefix: string) {
    for (const k of this.cache.keys()) if (k.startsWith(prefix)) this.cache.delete(k);
  }

  private async items(): Promise<string> {
    if (this.itemsPath) return this.itemsPath;
    const projects = await this.projects();
    if (projects.length === 0) return (this.itemsPath = "work-items");
    try {
      await this.request("GET", `${this.base}/projects/${projects[0].id}/work-items/?per_page=1`);
      this.itemsPath = "work-items";
    } catch (e) {
      if (e instanceof PlaneError && e.status === 404) this.itemsPath = "issues";
      else throw e;
    }
    return this.itemsPath;
  }

  // ---------- reads ----------

  me() {
    return this.cached("me", 3600_000, () => this.request<Member>("GET", `${this.baseUrl}/api/v1/users/me/`));
  }

  async projects(): Promise<Project[]> {
    const all = await this.cached("projects", 600_000, () => this.listAll<Project>("/projects/"));
    const follow = config.plane.projects.map((p) => p.toUpperCase());
    return follow.length ? all.filter((p) => follow.includes(p.identifier.toUpperCase())) : all;
  }

  states(projectId: string) {
    return this.cached(`states:${projectId}`, 600_000, () => this.listAll<State>(`/projects/${projectId}/states/`));
  }

  members(projectId: string) {
    return this.cached(`members:${projectId}`, 600_000, async () => {
      const raw = await this.listAll<Member & { member?: Member }>(`/projects/${projectId}/project-members/`);
      // Normalise: some versions wrap the user in `member`.
      return raw.map((m) => (m.member && typeof m.member === "object" ? m.member : m));
    });
  }

  labels(projectId: string) {
    return this.cached(`labels:${projectId}`, 600_000, () => this.listAll<Label>(`/projects/${projectId}/labels/`));
  }

  cycles(projectId: string) {
    return this.cached(`cycles:${projectId}`, 300_000, () =>
      this.listAll<Cycle>(`/projects/${projectId}/cycles/`, { cycle_view: "all" }),
    );
  }

  modules(projectId: string) {
    return this.cached(`modules:${projectId}`, 300_000, () => this.listAll<Module>(`/projects/${projectId}/modules/`));
  }

  async cycleLinks(projectId: string, cycleId: string): Promise<CycleLink[]> {
    return this.cached(`cycleitems:${projectId}:${cycleId}`, 120_000, async () => {
      const raw = await this.listAll<{ issue?: string | { id: string }; id: string; created_at: string }>(
        `/projects/${projectId}/cycles/${cycleId}/cycle-issues/`,
      );
      return raw.map((r) => ({ issueId: idOf(r.issue) ?? r.id, addedAt: r.created_at }));
    });
  }

  async moduleItemIds(projectId: string, moduleId: string): Promise<string[]> {
    return this.cached(`moduleitems:${projectId}:${moduleId}`, 120_000, async () => {
      const raw = await this.listAll<{ issue?: string | { id: string }; id: string }>(
        `/projects/${projectId}/modules/${moduleId}/module-issues/`,
      );
      return raw.map((r) => idOf(r.issue) ?? r.id);
    });
  }

  /** All work items of a project (cached 2 minutes). */
  async workItems(project: Project): Promise<WorkItem[]> {
    return this.cached(`items:${project.id}`, 120_000, async () => {
      const path = await this.items();
      const raw = await this.listAll<RawItem>(`/projects/${project.id}/${path}/`);
      return raw.map((r) => normalise(r, project));
    });
  }

  async workItemByKey(key: string): Promise<WorkItem> {
    const [ident, seq] = splitKey(key);
    const project = (await this.projects()).find((p) => p.identifier.toUpperCase() === ident);
    if (!project) throw new PlaneError(404, `ไม่พบโปรเจกต์ที่มีรหัส ${ident}`);
    const path = await this.items();
    const raw = await this.request<RawItem>("GET", `${this.base}/${path}/${ident}-${seq}/`);
    return normalise(raw, project);
  }

  // ---------- writes ----------

  async createWorkItem(project: Project, body: WorkItemWrite): Promise<WorkItem> {
    const path = await this.items();
    const raw = await this.request<RawItem>("POST", `${this.base}/projects/${project.id}/${path}/`, clean(body));
    this.invalidate(`items:${project.id}`);
    return normalise(raw, project);
  }

  async updateWorkItem(project: Project, id: string, body: WorkItemWrite): Promise<WorkItem> {
    const path = await this.items();
    const raw = await this.request<RawItem>("PATCH", `${this.base}/projects/${project.id}/${path}/${id}/`, clean(body));
    this.invalidate(`items:${project.id}`);
    return normalise(raw, project);
  }

  async addToCycle(projectId: string, cycleId: string, itemIds: string[]) {
    await this.request("POST", `${this.base}/projects/${projectId}/cycles/${cycleId}/cycle-issues/`, { issues: itemIds });
    this.invalidate(`cycleitems:${projectId}:${cycleId}`);
  }

  async addComment(project: Project, itemId: string, html: string) {
    const path = await this.items();
    await this.request("POST", `${this.base}/projects/${project.id}/${path}/${itemId}/comments/`, { comment_html: html });
  }

  itemUrl(item: WorkItem): string {
    return `${this.baseUrl}/${this.workspace}/browse/${item.key}/`;
  }

  projectUrl(project: Project): string {
    return `${this.baseUrl}/${this.workspace}/projects/${project.id}/issues`;
  }
}

export interface WorkItemWrite {
  name?: string;
  description_html?: string;
  state?: string;
  assignees?: string[];
  priority?: "urgent" | "high" | "medium" | "low" | "none";
  start_date?: string | null;
  target_date?: string | null;
  parent?: string | null;
  labels?: string[];
}

interface RawItem {
  id: string;
  name: string;
  sequence_id: number;
  priority?: string;
  state?: string | { id: string };
  state_id?: string;
  assignees?: (string | { id: string })[];
  assignee_ids?: string[];
  labels?: (string | { id: string })[];
  label_ids?: string[];
  start_date?: string | null;
  target_date?: string | null;
  completed_at?: string | null;
  created_at: string;
  updated_at?: string;
  parent?: string | { id: string } | null;
  parent_id?: string | null;
}

function idOf(v: string | { id: string } | null | undefined): string | undefined {
  if (!v) return undefined;
  return typeof v === "string" ? v : v.id;
}

function normalise(r: RawItem, project: Project): WorkItem {
  return {
    id: r.id,
    projectId: project.id,
    key: `${project.identifier}-${r.sequence_id}`,
    name: r.name,
    priority: r.priority ?? "none",
    stateId: idOf(r.state) ?? r.state_id ?? "",
    assigneeIds: (r.assignees ?? []).map((a) => idOf(a)!).filter(Boolean).concat(r.assignee_ids ?? []),
    labelIds: (r.labels ?? []).map((l) => idOf(l)!).filter(Boolean).concat(r.label_ids ?? []),
    startDate: r.start_date ?? null,
    targetDate: r.target_date ?? null,
    completedAt: r.completed_at ?? null,
    createdAt: r.created_at,
    updatedAt: r.updated_at ?? r.created_at,
    parentId: idOf(r.parent) ?? r.parent_id ?? null,
  };
}

export function splitKey(key: string): [string, number] {
  const m = key.trim().toUpperCase().match(/^([A-Z0-9]+)-(\d+)$/);
  if (!m) throw new PlaneError(400, `รหัสงาน "${key}" ไม่ถูกรูปแบบ (เช่น FDABIGDATA-123)`);
  return [m[1], Number(m[2])];
}

function clean<T extends object>(o: T): Partial<T> {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined)) as Partial<T>;
}
