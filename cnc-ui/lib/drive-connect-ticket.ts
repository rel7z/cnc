import { randomBytes } from "node:crypto";

interface DriveConnectTicket {
  authorization: string;
  expiresAt: number;
}

const ticketGlobal = globalThis as typeof globalThis & {
  __cncDriveConnectTickets?: Map<string, DriveConnectTicket>;
};

function tickets() {
  ticketGlobal.__cncDriveConnectTickets ??= new Map<string, DriveConnectTicket>();
  const now = Date.now();
  for (const [ticket, record] of ticketGlobal.__cncDriveConnectTickets) {
    if (record.expiresAt <= now) ticketGlobal.__cncDriveConnectTickets.delete(ticket);
  }
  return ticketGlobal.__cncDriveConnectTickets;
}

export function createDriveConnectTicket(authorization: string): string {
  const store = tickets();
  while (store.size >= 100) store.delete(store.keys().next().value as string);
  const ticket = randomBytes(24).toString("base64url");
  store.set(ticket, { authorization, expiresAt: Date.now() + 10 * 60 * 1000 });
  return ticket;
}

export function consumeDriveConnectTicket(ticket: string): string | null {
  if (!/^[A-Za-z0-9_-]{32}$/.test(ticket)) return null;
  const store = tickets();
  const record = store.get(ticket);
  store.delete(ticket);
  return record?.authorization ?? null;
}

export async function loadControllerDriveAuthorization(): Promise<URL> {
  const api = process.env.GO_API_URL || "http://localhost:8080";
  const response = await fetch(`${api}/api/config/gdrive/auth-url`, { cache: "no-store", signal: AbortSignal.timeout(10000) });
  if (!response.ok) throw new Error((await response.text()) || `Controller returned ${response.status}.`);
  const payload: { url?: string } = await response.json();
  if (!payload.url) throw new Error("Controller did not return an authorization URL.");
  let authorization = new URL(payload.url);
  if (authorization.pathname === "/drive-connect") {
    const nested = authorization.searchParams.get("authorization");
    if (!nested) throw new Error("Controller returned an incomplete authorization bridge.");
    authorization = new URL(nested);
  }
  return authorization;
}
