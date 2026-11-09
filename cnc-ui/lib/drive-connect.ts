export const DRIVE_STATE_COOKIE = "cnc_drive_state";
export const DRIVE_STATE_COOKIE_PATH = "/api/config/gdrive";

export interface DriveBridge {
  authorization: URL;
  state: string;
  secure: boolean;
}

export function buildDriveBridgeURL(callbackValue: string, authorizationValue: string): string {
  const callback = new URL(callbackValue);
  const authorization = new URL(authorizationValue);
  const state = authorization.searchParams.get("state");
  if (authorization.protocol !== "https:" || authorization.hostname !== "accounts.google.com" || !authorization.pathname.startsWith("/o/oauth2/") || !state || !/^[A-Za-z0-9_-]{43}$/.test(state)) {
    throw new Error("Google returned an invalid authorization link.");
  }
  if (authorization.searchParams.get("redirect_uri") !== callback.href || callback.pathname !== "/api/config/gdrive/callback" || callback.search || callback.hash) {
    throw new Error("Google authorization does not match the saved callback.");
  }
  const bridge = new URL("/drive-connect", callback.origin);
  bridge.searchParams.set("authorization", authorization.href);
  bridge.searchParams.set("state", state);
  return bridge.href;
}

export function decodeDriveAuthorizationPayload(payload: string): string {
  if (!/^[A-Za-z0-9_-]{1,4096}$/.test(payload)) throw new Error("Authorization payload is invalid.");
  const decoded = Buffer.from(payload, "base64url").toString("utf8");
  if (!decoded || Buffer.from(decoded, "utf8").toString("base64url") !== payload) {
    throw new Error("Authorization payload is invalid.");
  }
  return decoded;
}

export function driveBridgeRequestURL(internalURL: string, hostHeader: string | null, protocolHeader: string | null): string {
  const request = new URL(internalURL);
  const host = hostHeader?.split(",", 1)[0]?.trim();
  const protocol = protocolHeader?.split(",", 1)[0]?.trim().replace(/:$/, "") || request.protocol.replace(/:$/, "");
  if (!host || (protocol !== "http" && protocol !== "https")) return request.href;
  const origin = new URL(`${protocol}://${host}`);
  if (origin.username || origin.password || origin.pathname !== "/" || origin.search || origin.hash) {
    throw new Error("Browser-facing proxy origin is invalid.");
  }
  request.protocol = origin.protocol;
  request.host = origin.host;
  return request.href;
}

export function validateDriveBridge(requestURL: string, authorizationValue: string | null, stateValue: string | null): DriveBridge {
  if (!authorizationValue || !stateValue || !/^[A-Za-z0-9_-]{43}$/.test(stateValue)) {
    throw new Error("Authorization link is missing or expired.");
  }
  const request = new URL(requestURL);
  const authorization = new URL(authorizationValue);
  if (authorization.protocol !== "https:" || authorization.hostname !== "accounts.google.com" || !authorization.pathname.startsWith("/o/oauth2/")) {
    throw new Error("Authorization destination is invalid.");
  }
  if (authorization.searchParams.get("state") !== stateValue) {
    throw new Error("Authorization state does not match.");
  }
  const callbackValue = authorization.searchParams.get("redirect_uri");
  if (!callbackValue) throw new Error("Authorization callback is missing.");
  const callback = new URL(callbackValue);
  if (callback.origin !== request.origin || callback.pathname !== "/api/config/gdrive/callback" || callback.search || callback.hash) {
    throw new Error("Authorization callback does not match this tunnel.");
  }
  return { authorization, state: stateValue, secure: request.protocol === "https:" };
}
