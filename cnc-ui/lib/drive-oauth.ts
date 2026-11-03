export const DEFAULT_DRIVE_CALLBACK = "http://localhost:8080/api/config/gdrive/callback";

// A dashboard tunnel port is temporary. Google's registered callback is not.
export async function driveConnectionURL(callbackValue: string): Promise<string> {
  const callback = new URL(callbackValue || DEFAULT_DRIVE_CALLBACK);
  if (["localhost", "127.0.0.1", "[::1]"].includes(callback.hostname) && callback.origin !== window.location.origin) {
    try {
      const health = await fetch(`${callback.origin}/_cnc-drive/health`, { cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(5000) });
      const status = await health.json();
      if (!health.ok || status.protocol !== "cnc-drive-callback-v1" || status.callback_url !== callback.href) throw new Error();
    } catch {
      throw new Error("Open CNC Manager and reconnect. Its fixed Google callback is not ready; do not change the callback to the dashboard port.");
    }
  }
  const response = await fetch("/api/drive-connect-ticket", { method: "POST", signal: AbortSignal.timeout(15000) });
  if (!response.ok) throw new Error((await response.text()) || "Could not start Google authorization.");
  const payload = await response.json();
  const bridge = new URL(payload.url);
  if (bridge.origin !== callback.origin || bridge.pathname !== "/drive-connect") throw new Error("Google callback differs from the saved connection settings. Reload Drive settings and retry.");
  return bridge.href;
}
