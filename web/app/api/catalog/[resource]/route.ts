import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const ALLOWED = [
  "stats",
  "sites",
  "observations",
  "intel-search",
  "intel-source",
  "intel-stats",
  "intel-scan",
  "intel-status",
];

function getBackendUrl(resource: string): URL {
  const base = new URL(process.env.INTERNET_MAP_API_URL || "http://127.0.0.1:3500");
  if (base.protocol !== "https:" && !(base.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(base.hostname))) {
    throw Error("Invalid API origin");
  }
  if (base.username || base.password) throw Error("Invalid API origin");

  let path = "/api/v1/" + resource;
  if (resource.startsWith("intel-")) {
    path = "/api/v1/intel/" + resource.slice(6);
  }
  return new URL(path, base);
}

export async function GET(request: NextRequest, context: { params: Promise<{ resource: string }> }) {
  const { resource } = await context.params;
  if (!ALLOWED.includes(resource)) {
    return Response.json({ error: "Unknown resource" }, { status: 404 });
  }

  const key = process.env.INTERNET_MAP_API_KEY;
  if (!key) {
    return Response.json({ error: "Configure the catalog API connection on the application server." }, { status: 503 });
  }

  try {
    const url = getBackendUrl(resource);
    for (const name of ["host", "page", "wordpress", "url", "limit", "q"]) {
      const val = request.nextUrl.searchParams.get(name);
      if (val !== null) url.searchParams.set(name, val);
    }
    const response = await fetch(url, {
      headers: { Authorization: "Bearer " + key },
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.any([request.signal, AbortSignal.timeout(15000)]),
    });
    return new Response(response.body, {
      status: response.status,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
    });
  } catch {
    return Response.json({ error: "Catalog service is unavailable." }, { status: 503 });
  }
}

export async function POST(request: NextRequest, context: { params: Promise<{ resource: string }> }) {
  const { resource } = await context.params;
  if (!ALLOWED.includes(resource)) {
    return Response.json({ error: "Unknown resource" }, { status: 404 });
  }

  const key = process.env.INTERNET_MAP_API_KEY;
  if (!key) {
    return Response.json({ error: "Configure the catalog API connection on the application server." }, { status: 503 });
  }

  try {
    const url = getBackendUrl(resource);
    for (const name of ["url", "limit"]) {
      const val = request.nextUrl.searchParams.get(name);
      if (val !== null) url.searchParams.set(name, val);
    }
    let bodyData = null;
    const contentType = request.headers.get("content-type") || "";
    if (contentType.includes("application/json")) {
      bodyData = await request.text();
    }
    const response = await fetch(url, {
      method: "POST",
      headers: {
        Authorization: "Bearer " + key,
        "Content-Type": "application/json",
      },
      body: bodyData,
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.any([request.signal, AbortSignal.timeout(20000)]),
    });
    return new Response(response.body, {
      status: response.status,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
    });
  } catch {
    return Response.json({ error: "Catalog service is unavailable." }, { status: 503 });
  }
}
