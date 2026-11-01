// Notion: search meeting notes / decisions, read a page as text, create pages (retro, CR).
// Uses an internal integration token entered on the Settings page; share pages with the integration in Notion.
import { store } from "./store.js";
import { decrypt } from "./crypto.js";

const VERSION = "2022-06-28";

export class NotionNotConnected extends Error {
  constructor() { super("ยังไม่ได้เชื่อม Notion ไปที่หน้าตั้งค่า > การเชื่อมต่อ ก่อนนะครับ"); }
}

export const notionConnected = () => !!store.get().connections.notion?.tokenEnc;

async function api<T>(path: string, init: RequestInit = {}, tokenOverride?: string): Promise<T> {
  const token = tokenOverride ?? decrypt(store.get().connections.notion?.tokenEnc);
  if (!token) throw new NotionNotConnected();
  const r = await fetch(`https://api.notion.com/v1${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${token}`, "Notion-Version": VERSION, "Content-Type": "application/json", ...(init.headers ?? {}) },
  });
  const text = await r.text();
  if (!r.ok) {
    let msg = text.slice(0, 200);
    try { msg = JSON.parse(text).message ?? msg; } catch { /* keep raw */ }
    throw new Error(`Notion ${r.status}: ${msg}`);
  }
  return JSON.parse(text) as T;
}

export async function notionWhoami(token: string): Promise<string> {
  const j = await api<{ name?: string; bot?: { workspace_name?: string } }>("/users/me", {}, token);
  return [j.name, j.bot?.workspace_name].filter(Boolean).join(" · ");
}

export interface NotionPage { id: string; title: string; url: string; editedAt: string }

function pageTitle(p: any): string {
  for (const v of Object.values(p.properties ?? {}) as any[]) {
    if (v?.type === "title") return (v.title ?? []).map((t: any) => t.plain_text).join("") || "(ไม่มีชื่อ)";
  }
  return "(ไม่มีชื่อ)";
}

export async function searchPages(query: string, limit = 5): Promise<NotionPage[]> {
  const j = await api<{ results: any[] }>("/search", {
    method: "POST",
    body: JSON.stringify({ query, filter: { property: "object", value: "page" }, sort: { direction: "descending", timestamp: "last_edited_time" }, page_size: limit }),
  });
  return j.results.map((p) => ({ id: p.id, title: pageTitle(p), url: p.url, editedAt: p.last_edited_time }));
}

const rich = (arr: any[] | undefined) => (arr ?? []).map((t) => t.plain_text).join("");

/** Page content as plain text (headings, bullets, to-dos, paragraphs), one level of children. */
export async function pageText(id: string, maxChars = 6000): Promise<{ title: string; url: string; text: string }> {
  const page = await api<any>(`/pages/${id}`);
  const lines: string[] = [];
  let cursor: string | undefined;
  do {
    const j = await api<{ results: any[]; next_cursor: string | null; has_more: boolean }>(`/blocks/${id}/children?page_size=100${cursor ? `&start_cursor=${cursor}` : ""}`);
    for (const b of j.results) {
      const v = b[b.type] ?? {};
      const t = rich(v.rich_text);
      if (b.type.startsWith("heading")) lines.push(`\n## ${t}`);
      else if (b.type === "bulleted_list_item" || b.type === "numbered_list_item") lines.push(`• ${t}`);
      else if (b.type === "to_do") lines.push(`${v.checked ? "[x]" : "[ ]"} ${t}`);
      else if (t) lines.push(t);
    }
    cursor = j.has_more ? j.next_cursor ?? undefined : undefined;
  } while (cursor && lines.join("\n").length < maxChars);
  return { title: pageTitle(page), url: page.url, text: lines.join("\n").slice(0, maxChars) };
}

type Block = Record<string, unknown>;
const rt = (s: string) => [{ type: "text", text: { content: s.slice(0, 1900) } }];

/** Light markdown → Notion blocks: "# ", "## ", "- " / "• ", "[ ] ", plain lines. */
export function toBlocks(md: string): Block[] {
  const out: Block[] = [];
  for (const raw of md.split("\n")) {
    const l = raw.trimEnd();
    if (!l.trim()) continue;
    let m;
    if ((m = l.match(/^#\s+(.*)/))) out.push({ object: "block", type: "heading_2", heading_2: { rich_text: rt(m[1]) } });
    else if ((m = l.match(/^##+\s+(.*)/))) out.push({ object: "block", type: "heading_3", heading_3: { rich_text: rt(m[1]) } });
    else if ((m = l.match(/^\s*\[( |x)\]\s+(.*)/i))) out.push({ object: "block", type: "to_do", to_do: { rich_text: rt(m[2]), checked: m[1].toLowerCase() === "x" } });
    else if ((m = l.match(/^\s*[-•*]\s+(.*)/))) out.push({ object: "block", type: "bulleted_list_item", bulleted_list_item: { rich_text: rt(m[1]) } });
    else out.push({ object: "block", type: "paragraph", paragraph: { rich_text: rt(l) } });
  }
  return out.slice(0, 100);
}

export async function createPage(parentPageId: string, title: string, markdown: string): Promise<NotionPage> {
  if (!parentPageId) throw new Error("ยังไม่ได้ตั้งหน้า Notion ที่จะเก็บเอกสาร (ตั้งค่า > การแจ้งเตือน > หน้า Notion สำหรับเอกสาร)");
  const p = await api<any>("/pages", {
    method: "POST",
    body: JSON.stringify({ parent: { page_id: normalizeId(parentPageId) }, properties: { title: { title: rt(title) } }, children: toBlocks(markdown) }),
  });
  return { id: p.id, title, url: p.url, editedAt: p.last_edited_time };
}

export async function appendToPage(pageId: string, markdown: string) {
  await api(`/blocks/${normalizeId(pageId)}/children`, { method: "PATCH", body: JSON.stringify({ children: toBlocks(markdown) }) });
}

/** Accepts a page id or a notion.so URL. */
export function normalizeId(s: string): string {
  const m = s.replace(/-/g, "").match(/([0-9a-f]{32})(?:\?|$|#)/i) ?? s.replace(/-/g, "").match(/([0-9a-f]{32})/i);
  return m ? m[1] : s;
}
