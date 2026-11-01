// LINE Flex Message builders, matching the screens in the design canvas.
import type { messagingApi } from "@line/bot-sdk";
import type { ProjectMetrics, ItemView } from "./metrics.js";
import { thaiDate } from "./metrics.js";
import type { Preview } from "./tools.js";

type Msg = messagingApi.Message;
type Box = Record<string, unknown>;

export const ACCENT = "#2448C8";
const MUTED = "#5A6573";
const RED = "#A2231B";
const AMBER = "#8A4300";
const GREEN = "#1B6B34";

const text = (t: string, o: Box = {}): Box => ({ type: "text", text: t || " ", wrap: true, size: "sm", color: "#18202B", ...o });
const sep = (): Box => ({ type: "separator", margin: "md", color: "#E6EAF0" });

function button(label: string, action: Box, primary = false): Box {
  return { type: "button", style: primary ? "primary" : "secondary", color: primary ? ACCENT : undefined, height: "sm", action: { label: label.slice(0, 40), ...action } };
}
const uri = (label: string, url: string, primary = false) => button(label, { type: "uri", uri: url }, primary);
const postback = (label: string, data: string, primary = false, displayText?: string) =>
  button(label, { type: "postback", data, displayText: displayText ?? label }, primary);

function bar(percent: number, color = ACCENT): Box {
  const p = Math.max(0, Math.min(100, Math.round(percent)));
  return {
    type: "box", layout: "vertical", height: "8px", backgroundColor: "#E6EAF0", cornerRadius: "4px", margin: "sm",
    contents: [{ type: "box", layout: "vertical", width: `${Math.max(p, 1)}%`, height: "8px", backgroundColor: color, cornerRadius: "4px", contents: [] }],
  };
}

function stat(n: number, label: string, color: string, bg: string): Box {
  return {
    type: "box", layout: "vertical", backgroundColor: bg, cornerRadius: "8px", paddingAll: "8px", flex: 1,
    contents: [text(String(n), { size: "lg", weight: "bold", color }), text(label, { size: "xs", color })],
  };
}

function itemLine(v: ItemView, extra?: string): Box {
  return text(`${v.key} ${v.name}${extra ? ` · ${extra}` : ""}`, { size: "sm", maxLines: 2 });
}

function bubble(header: Box[] | null, body: Box[], footer: Box[] | null, headerBg?: string): Box {
  return {
    type: "bubble", size: "mega",
    ...(header ? { header: { type: "box", layout: "vertical", spacing: "xs", paddingAll: "14px", backgroundColor: headerBg ?? "#EEF2FD", contents: header } } : {}),
    body: { type: "box", layout: "vertical", spacing: "sm", paddingAll: "14px", contents: body },
    ...(footer ? { footer: { type: "box", layout: "vertical", spacing: "sm", paddingAll: "14px", contents: footer } } : {}),
  };
}

const flex = (altText: string, contents: Box): Msg => ({ type: "flex", altText: altText.slice(0, 390), contents } as unknown as Msg);

// ------------------------------------------------------------------ cards

export function statusCard(m: ProjectMetrics, dashboardUrl: string): Msg {
  const c = m.cycle;
  const body: Box[] = [];
  if (c) {
    body.push(
      { type: "box", layout: "horizontal", contents: [text(`เสร็จ ${c.done} จาก ${c.total} งาน`, { color: MUTED, flex: 1 }), text(`${c.percent}%`, { weight: "bold", size: "lg", align: "end", flex: 0 })] },
      bar(c.percent),
      text(`เหลือ ${c.daysLeft} วัน${c.projectedMiss ? ` · ตาม velocity คาดว่าหลุด ${c.projectedMiss} งาน` : " · ตามแผน"}`, { size: "xs", color: c.projectedMiss ? AMBER : MUTED }),
    );
    if (c.addedMidway) body.push(text(`เพิ่มกลาง sprint ${c.addedMidway} งาน`, { size: "xs", color: MUTED }));
  }
  body.push({
    type: "box", layout: "horizontal", spacing: "sm", margin: "md",
    contents: [stat(m.counts.overdue, "เลยกำหนด", RED, "#FDECEA"), stat(m.counts.blocked, "Blocked", AMBER, "#FFF1E0"), stat(m.counts.unassigned, "ไม่มีเจ้าของ", "#2E3947", "#EEF1F5")],
  });
  const first = m.overdue[0] ?? m.blocked[0];
  if (first) {
    body.push(sep(), text(first.daysOverdue ? `ควรดูก่อน · เลยกำหนด ${first.daysOverdue} วัน` : "ควรดูก่อน · Blocked", { size: "xs", weight: "bold", color: RED, margin: "md" }), itemLine(first));
  }
  return flex(
    `${m.project.name}: ${c ? `${c.percent}% ` : ""}เลยกำหนด ${m.counts.overdue}`,
    bubble(
      [text(`${m.project.name} · ${c ? "Cycle ปัจจุบัน" : "ภาพรวม"}`, { size: "xs", color: MUTED }), text(c ? `${c.name} · ${thaiDate(c.start, false)} – ${thaiDate(c.end, false)}` : `งานเปิด ${m.counts.open} งาน`, { weight: "bold", size: "md" })],
      body,
      [
        ...(m.counts.overdue ? [postback("ดูงานที่เลยกำหนด", "cmd:overdue", true, "งานไหนเลยกำหนดบ้าง")] : []),
        uri("Dashboard", dashboardUrl, !m.counts.overdue),
      ],
    ),
  );
}

export function digestCard(list: ProjectMetrics[], dateLabel: string, dashboardUrl: string, meetings: string[] = []): Msg {
  const overdue = list.flatMap((m) => m.overdue);
  const today = list.flatMap((m) => m.dueToday);
  const body: Box[] = [];
  const section = (title: string, color: string, items: Box[]) => {
    if (body.length) body.push(sep());
    body.push(text(title, { size: "xs", weight: "bold", color, margin: "md" }), ...items);
  };
  section(`เลยกำหนด ${overdue.length}`, overdue.length ? RED : MUTED, overdue.slice(0, 3).map((v) => itemLine(v, `${v.daysOverdue} วัน`)));
  if (overdue.length > 3) body.push(text(`+ อีก ${overdue.length - 3} งาน`, { size: "xs", color: ACCENT }));
  section(`ครบกำหนดวันนี้ ${today.length}`, "#3D4855", today.slice(0, 3).map((v) => itemLine(v)));
  if (meetings.length) section(`ประชุมวันนี้ ${meetings.length}`, "#3D4855", meetings.map((t) => text(t)));
  const withCycle = list.filter((m) => m.cycle);
  if (withCycle.length) {
    body.push(sep());
    for (const m of withCycle) {
      body.push({ type: "box", layout: "horizontal", margin: "md", contents: [text(m.project.name, { flex: 1 }), text(`${m.cycle!.percent}%`, { weight: "bold", align: "end", flex: 0 })] }, bar(m.cycle!.percent));
    }
  }
  return flex(
    `สรุปเช้านี้: เลยกำหนด ${overdue.length}, ครบวันนี้ ${today.length}`,
    bubble(
      [text("สรุปเช้านี้", { size: "xs", color: "#DCE4FB" }), text(dateLabel, { weight: "bold", size: "lg", color: "#FFFFFF" })],
      body,
      [{ type: "box", layout: "horizontal", spacing: "sm", contents: [postback("งานของฉัน", "cmd:mine", true, "งานของฉันมีอะไรบ้าง"), uri("Dashboard", dashboardUrl)] }],
      ACCENT,
    ),
  );
}

/** Confirm card. `proactive` cards (sent by the bot on its own) use pconfirm/pedit/pcancel. */
export function confirmCard(p: Preview, pendingId: string, proactive = false, previewUrl?: string): Msg {
  const pre = proactive ? "p" : "";
  const lines: Box[] = p.lines.map((l) => ({
    type: "box", layout: "horizontal", spacing: "md", paddingTop: "6px", paddingBottom: "6px",
    contents: [
      ...(l.tag ? [{ type: "box", layout: "vertical", backgroundColor: "#E8EDFC", cornerRadius: "6px", width: "44px", justifyContent: "center", contents: [text(l.tag, { size: "xxs", weight: "bold", align: "center", color: "#2A3A7A" })] }] : []),
      { type: "box", layout: "vertical", flex: 1, contents: [text(l.text, { weight: "bold" }), text(l.sub, { size: "xs", color: l.warn ? AMBER : MUTED })] },
    ],
  }));
  const warn: Box[] = p.warnings.length
    ? [{ type: "box", layout: "vertical", backgroundColor: "#FFF1E0", cornerRadius: "8px", paddingAll: "8px", margin: "md", contents: [text(p.warnings.join("\n"), { size: "xs", color: "#6B3400" })] }]
    : [];
  return flex(
    `รอยืนยัน: ${p.title}`,
    bubble(
      [text(p.kicker ?? "รอยืนยัน", { size: "xs", weight: "bold", color: "#2A3A7A" }), text(p.title, { weight: "bold", size: "md" }), text(p.subtitle, { size: "xs", color: "#4A5568" })],
      [...lines, ...warn],
      [
        postback(p.confirmLabel, `${pre}confirm:${pendingId}`, true),
        ...(previewUrl ? [uri("ดูฉบับเต็ม", previewUrl)] : []),
        { type: "box", layout: "horizontal", spacing: "sm", contents: [postback("แก้ไข", `${pre}edit:${pendingId}`, false, "ขอแก้ไข"), postback(p.cancelLabel ?? "ยกเลิก", `${pre}cancel:${pendingId}`, false)] },
      ],
    ),
  );
}

export function resultCard(title: string, links: { label: string; url: string }[]): Msg {
  return flex(
    title,
    bubble(null, [text(title, { weight: "bold", color: GREEN }), ...links.map((l) => ({ ...text(l.label, { color: ACCENT, decoration: "underline" }), action: { type: "uri", uri: l.url } }))], null),
  );
}

export function projectPicker(prompt: string, projects: { id: string; name: string }[]): Msg {
  return {
    type: "text",
    text: prompt,
    quickReply: {
      items: projects.slice(0, 12).map((p) => ({
        type: "action",
        action: { type: "postback", label: p.name.slice(0, 20), data: `project:${p.id}`, displayText: p.name },
      })),
    },
  } as unknown as Msg;
}

export function textMsg(t: string): Msg {
  return { type: "text", text: t.slice(0, 5000) } as Msg;
}

// ------------------------------------------------------------------ generic info card

export type Btn = { label: string; data?: string; url?: string; primary?: boolean; displayText?: string };
export interface InfoCard {
  kicker: string;
  title: string;
  subtitle?: string;
  tone?: "info" | "warn" | "danger" | "ok";
  rows?: { label: string; value: string; color?: "red" | "amber" | "green" | "muted" }[];
  bars?: { label: string; percent: number; value?: string; tone?: "red" | "amber" | "green" }[];
  sections?: { title: string; lines: string[]; tone?: "red" | "amber" | "green" | "muted" }[];
  note?: string;
  buttons?: Btn[];
}

const TONE = { red: RED, amber: AMBER, green: GREEN, muted: MUTED } as const;
const HEAD: Record<NonNullable<InfoCard["tone"]>, string> = { info: "#EEF2FD", warn: "#FFF1E0", danger: "#FDECEA", ok: "#E7F4EC" };
const HEAD_TEXT: Record<NonNullable<InfoCard["tone"]>, string> = { info: "#2A3A7A", warn: "#6B3400", danger: RED, ok: GREEN };

function toButton(b: Btn): Box {
  if (b.url) return uri(b.label, b.url, b.primary);
  return postback(b.label, b.data ?? "", b.primary, b.displayText);
}

export function infoCard(c: InfoCard): Msg {
  const tone = c.tone ?? "info";
  const body: Box[] = [];
  for (const r of c.rows ?? []) {
    body.push({ type: "box", layout: "horizontal", spacing: "md", contents: [text(r.label, { size: "xs", color: MUTED, flex: 2 }), text(r.value, { size: "sm", flex: 5, weight: "bold", color: r.color ? TONE[r.color] : "#18202B" })] });
  }
  for (const b of c.bars ?? []) {
    const col = b.tone ? TONE[b.tone] : ACCENT;
    body.push({ type: "box", layout: "horizontal", margin: "md", contents: [text(b.label, { flex: 1, size: "sm" }), text(b.value ?? `${b.percent}%`, { flex: 0, align: "end", weight: "bold", color: b.tone ? col : "#18202B" })] }, bar(Math.min(100, b.percent), col));
  }
  for (const sct of c.sections ?? []) {
    if (!sct.lines.length) continue;
    if (body.length) body.push(sep());
    body.push(text(sct.title, { size: "xs", weight: "bold", color: sct.tone ? TONE[sct.tone] : "#3D4855", margin: "md" }));
    for (const l of sct.lines.slice(0, 8)) body.push(text(l, { size: "sm" }));
    if (sct.lines.length > 8) body.push(text(`+ อีก ${sct.lines.length - 8} รายการ`, { size: "xs", color: ACCENT }));
  }
  if (c.note) body.push({ type: "box", layout: "vertical", backgroundColor: "#F4F6F9", cornerRadius: "8px", paddingAll: "8px", margin: "md", contents: [text(c.note, { size: "xs", color: "#3D4855" })] });
  if (!body.length) body.push(text(" "));
  const btns = c.buttons ?? [];
  const footer: Box[] = [];
  if (btns[0]) footer.push(toButton(btns[0]));
  const rest = btns.slice(1, 5);
  for (let i = 0; i < rest.length; i += 2) {
    footer.push({ type: "box", layout: "horizontal", spacing: "sm", contents: rest.slice(i, i + 2).map(toButton) });
  }
  return flex(
    `${c.kicker}: ${c.title}`,
    bubble(
      [text(c.kicker, { size: "xs", weight: "bold", color: HEAD_TEXT[tone] }), text(c.title, { weight: "bold", size: "md" }), ...(c.subtitle ? [text(c.subtitle, { size: "xs", color: "#4A5568" })] : [])],
      body,
      footer.length ? footer : null,
      HEAD[tone],
    ),
  );
}

/** Text with quick-reply buttons (postbacks). */
export function quickText(t: string, items: { label: string; data: string; displayText?: string }[]): Msg {
  return {
    type: "text",
    text: t.slice(0, 5000),
    quickReply: { items: items.slice(0, 13).map((i) => ({ type: "action", action: { type: "postback", label: i.label.slice(0, 20), data: i.data.slice(0, 300), displayText: (i.displayText ?? i.label).slice(0, 300) } })) },
  } as unknown as Msg;
}
