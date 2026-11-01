// Turning what the owner typed ("FDA", "ต้น", "Blocked") into IDs of the connected task tool.
import { tasks } from "./providers/index.js";
import { splitKey, type Project, type Member } from "./plane.js";
import { memberName } from "./metrics.js";
import { store } from "./store.js";

export const plane = tasks;

export class UserFacingError extends Error {}

export function norm(s: string) {
  return s.toLowerCase().replace(/[\s_\-:]+/g, "");
}

export function escapeHtml(s: string) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}

export async function resolveProject(ref?: string): Promise<Project> {
  const projects = await plane.projects();
  if (!projects.length) throw new UserFacingError("ยังไม่มีโปรเจกต์ให้ใช้ (ยังไม่ได้เชื่อมระบบจัดการงาน หรือยังไม่ได้เลือกโปรเจกต์ที่ติดตามในหน้าตั้งค่า)");
  if (!ref) {
    const cur = projects.find((p) => p.id === store.get().currentProjectId);
    if (cur) return cur;
    if (projects.length === 1) return projects[0];
    throw new UserFacingError(`NEED_PROJECT: ยังไม่ได้เลือกโปรเจกต์ปัจจุบัน ให้ถามผู้ใช้ว่าหมายถึงโปรเจกต์ไหน: ${projects.map((p) => p.name).join(", ")}`);
  }
  const q = norm(ref);
  const hit =
    projects.find((p) => norm(p.identifier) === q) ??
    projects.find((p) => norm(p.name) === q) ??
    projects.find((p) => norm(p.name).includes(q) || q.includes(norm(p.identifier)) || norm(p.identifier).startsWith(q));
  if (!hit) throw new UserFacingError(`ไม่พบโปรเจกต์ "${ref}" ที่มี: ${projects.map((p) => `${p.name} (${p.identifier})`).join(", ")}`);
  return hit;
}

export async function projectOfKey(key: string): Promise<Project> {
  const [ident] = splitKey(key);
  const p = (await plane.projects()).find((x) => x.identifier.toUpperCase() === ident);
  if (!p) throw new UserFacingError(`ไม่พบโปรเจกต์ของรหัส ${key}`);
  return p;
}

export const nicknames = () => store.get().prefs.nicknames;

export async function resolveMember(projectId: string, who: string): Promise<Member | null> {
  if (who === "none") return null;
  const members = await plane.members(projectId);
  if (who === "me") {
    const me = await plane.me();
    return members.find((m) => m.id === me.id) ?? me;
  }
  const target = nicknames()[who] ?? who;
  const q = norm(target);
  const hit =
    members.find((m) => m.email && norm(m.email) === q) ??
    members.find((m) => [m.display_name, m.first_name, `${m.first_name ?? ""}${m.last_name ?? ""}`].some((n) => n && norm(n) === q)) ??
    members.find((m) => [m.display_name, m.first_name, m.email].some((n) => n && norm(n).includes(q)));
  if (!hit) {
    throw new UserFacingError(
      `ไม่พบสมาชิกชื่อ "${who}" ในโปรเจกต์ สมาชิกที่มี: ${members.map(memberName).join(", ")} (ตั้งชื่อเล่นได้ที่หน้าตั้งค่า > ทีมและชื่อเล่น)`,
    );
  }
  return hit;
}

/** Email for a person: an address as-is, a nickname mapped to an email, or a member's email. */
export async function resolveEmail(projectId: string | null, who: string): Promise<string> {
  const w = who.trim();
  if (/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(w)) return w;
  const mapped = nicknames()[w];
  if (mapped && mapped.includes("@")) return mapped;
  if (projectId) {
    const m = await resolveMember(projectId, w).catch(() => null);
    if (m?.email) return m.email;
  }
  throw new UserFacingError(`ไม่รู้อีเมลของ "${who}" ใส่เป็นอีเมลตรง ๆ หรือจับคู่ชื่อเล่นกับอีเมลในหน้าตั้งค่า > ทีมและชื่อเล่น`);
}

export async function resolveState(projectId: string, name: string) {
  const states = await plane.states(projectId);
  const q = norm(name);
  const hit =
    states.find((s) => norm(s.name) === q) ??
    states.find((s) => norm(s.name).includes(q)) ??
    states.find((s) => s.group === q || (q === "done" && s.group === "completed") || (q === "todo" && s.group === "unstarted"));
  if (!hit) throw new UserFacingError(`ไม่พบสถานะ "${name}" สถานะที่มี: ${states.map((s) => s.name).join(", ")}`);
  return hit;
}

export function checkDate(d: string | undefined, field: string) {
  if (d === undefined) return;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(d)) throw new UserFacingError(`${field} ต้องเป็น YYYY-MM-DD (ได้ "${d}")`);
}
