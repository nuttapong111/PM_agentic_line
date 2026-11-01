// Wall-clock helpers in the configured timezone (Asia/Bangkok by default).
import { config } from "./config.js";

function parts(d: Date) {
  const f = new Intl.DateTimeFormat("en-GB", {
    timeZone: config.timezone, year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, weekday: "short",
  });
  const o: Record<string, string> = {};
  for (const p of f.formatToParts(d)) o[p.type] = p.value;
  return o;
}

/** Minutes the zone is ahead of UTC at that instant. */
function offsetMinutes(d: Date): number {
  const o = parts(d);
  const asUtc = Date.UTC(+o.year, +o.month - 1, +o.day, +o.hour % 24, +o.minute, +o.second);
  return Math.round((asUtc - d.getTime()) / 60_000);
}

/** "2026-11-12", "10:00" in local time → Date. */
export function localToDate(ymd: string, hm: string): Date {
  const [y, m, d] = ymd.split("-").map(Number);
  const [h, mi] = hm.split(":").map(Number);
  const guess = new Date(Date.UTC(y, m - 1, d, h, mi));
  return new Date(guess.getTime() - offsetMinutes(guess) * 60_000);
}

/** Local "HH:MM". */
export function hm(d: Date = new Date()): string {
  const o = parts(d);
  return `${o.hour.padStart(2, "0").replace(/^24/, "00")}:${o.minute}`;
}

/** 0 = Sunday … 6 = Saturday, local. */
export function weekday(d: Date = new Date()): number {
  return ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(parts(d).weekday);
}

export function inQuietHours(from: string, to: string, now = hm()): boolean {
  if (!from || !to || from === to) return false;
  return from < to ? now >= from && now < to : now >= from || now < to;
}

/** Accepts "2026-11-03T10:00", "2026-11-03 10:00" (local) or a full ISO string with offset. */
export function parseLocal(s: string): Date | null {
  const m = s.trim().match(/^(\d{4}-\d{2}-\d{2})[T ](\d{1,2}:\d{2})(?::\d{2})?$/);
  if (m) return localToDate(m[1], m[2].padStart(5, "0"));
  const d = new Date(s);
  return isNaN(d.getTime()) ? null : d;
}

/** ISO string with the local offset, e.g. 2026-11-12T10:00:00+07:00 (what Google expects with timeZone). */
export function localIso(d: Date): string {
  const off = offsetMinutes(d);
  const local = new Date(d.getTime() + off * 60_000).toISOString().slice(0, 19);
  const sign = off >= 0 ? "+" : "-";
  const a = Math.abs(off);
  return `${local}${sign}${String(Math.floor(a / 60)).padStart(2, "0")}:${String(a % 60).padStart(2, "0")}`;
}
