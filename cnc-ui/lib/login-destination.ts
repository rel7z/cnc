// Allow only known local destinations; never pass through an arbitrary URL.
export function loginDestination(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//") || /[\\\x00-\x20]/.test(value)) return "/dashboard";
  try {
    const url = new URL(value, "http://localhost");
    const path = url.pathname;
    if (path === "/dashboard" || path.startsWith("/dashboard/") || path === "/drive-recover" || path === "/drive-local-start") {
      return path + url.search + url.hash;
    }
  } catch { /* use the dashboard */ }
  return "/dashboard";
}
