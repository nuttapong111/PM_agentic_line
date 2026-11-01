// AES-256-GCM for secrets saved from the Settings page (API tokens, OAuth refresh tokens).
import crypto from "node:crypto";
import { config } from "./config.js";

const key = () => crypto.createHash("sha256").update(`pm-assistant:${config.encryptionKey}`).digest();

export function encrypt(plain: string): string {
  const iv = crypto.randomBytes(12);
  const c = crypto.createCipheriv("aes-256-gcm", key(), iv);
  const data = Buffer.concat([c.update(plain, "utf8"), c.final()]);
  return ["v1", iv.toString("base64url"), c.getAuthTag().toString("base64url"), data.toString("base64url")].join(".");
}

export function decrypt(blob: string | undefined | null): string | null {
  if (!blob) return null;
  try {
    const [v, iv, tag, data] = blob.split(".");
    if (v !== "v1") return null;
    const d = crypto.createDecipheriv("aes-256-gcm", key(), Buffer.from(iv, "base64url"));
    d.setAuthTag(Buffer.from(tag, "base64url"));
    return Buffer.concat([d.update(Buffer.from(data, "base64url")), d.final()]).toString("utf8");
  } catch {
    console.error("decrypt failed: ENCRYPTION_KEY changed? re-enter the secret in Settings");
    return null;
  }
}

export const last4 = (s: string) => s.slice(-4);
export const randomId = (bytes = 6) => crypto.randomBytes(bytes).toString("hex");
