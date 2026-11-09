import { createHmac, createHash, timingSafeEqual, randomBytes } from "node:crypto";

export const SESSION_COOKIE = "cnc_session";
export const SESSION_SECONDS = 60 * 60 * 12;
export function credentials() {
  return { username: process.env.CNC_LOGIN_USERNAME || "fahrel", password: process.env.CNC_LOGIN_PASSWORD || "fahrel" };
}
function equal(a: string, b: string) {
  return timingSafeEqual(createHash("sha256").update(a).digest(), createHash("sha256").update(b).digest());
}
function sign(value: string) {
  const secret = process.env.CNC_SESSION_SECRET;
  if (!secret || secret.length < 32) throw new Error("Login is not configured");
  const c = credentials();
  return createHmac("sha256", secret).update(JSON.stringify([c.username,c.password,value])).digest("base64url");
}
export function checkCredentials(username: string, password: string) {
  const c = credentials();
  const userOK = equal(username,c.username); const passwordOK = equal(password,c.password);
  return userOK && passwordOK;
}
export function createSession() {
  const payload=Buffer.from(JSON.stringify({exp:Date.now()+SESSION_SECONDS*1000,nonce:randomBytes(16).toString("hex")})).toString("base64url");
  return `${payload}.${sign(payload)}`;
}
export function validSession(token?: string) {
  if (!token || token.length > 1024) return false;
  try {
    const [payload,signature,...extra]=token.split(".");
    if (!payload || !signature || extra.length || !equal(signature,sign(payload))) return false;
    const data=JSON.parse(Buffer.from(payload,"base64url").toString());
    return typeof data.exp === "number" && data.exp > Date.now();
  } catch { return false; }
}
export function sameOrigin(request: Request) {
  try { return new URL(request.headers.get("origin") || "").host === request.headers.get("host"); }
  catch { return false; }
}
