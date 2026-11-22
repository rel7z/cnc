import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const ALLOWED = [
  "database-sources",
  "database-stats",
  "database-sample",
  "database-maintenance",
  "database-import",
  "database-delete",
  "database-bundle-status",
  "database-bundle-create",
  "database-bundle-verify",
  "database-sync",
  "sources",
  "discovery-status",
  "discovery-workers",
  "discovery-control",
  "stats",
  "sites",
  "observations",
  "intel-search",
  "intel-source",
  "intel-stats",
  "intel-signatures",
  "intel-scan",
  "intel-status",
  "scanner",
  "scanner-status",
  "scanner-recent",
  "scanner-start",
  "scanner-stop",
  "scanner-resources",
  "scanner-resource",
  "cluster",
  "cluster-nodes",
  "cluster-start",
  "cluster-stop",
  "cluster-lease",
  "cluster-submit",
  "export",
  "download",
  "export.txt",
  "download.txt",
];

function getBackendUrl(resource: string): URL {
  const base = new URL(process.env.INTERNET_MAP_API_URL || "http://127.0.0.1:3500");
  if (base.protocol !== "https:" && !(base.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(base.hostname))) {
    throw new Error("Invalid API origin");
  }
  if (base.username || base.password) throw new Error("Invalid API origin");

  let path = "/api/v1/" + resource;
  if (resource.startsWith("database-")) {
    if (resource === "database-sample") {
      path = "/api/v1/database/sources/sample";
    } else if (resource === "database-delete") {
      path = "/api/v1/database/sources/delete";
    } else if (resource === "database-bundle-status") {
      path = "/api/v1/database/bundle/status";
    } else if (resource === "database-bundle-create") {
      path = "/api/v1/database/bundle/create";
    } else if (resource === "database-bundle-verify") {
      path = "/api/v1/database/bundle/verify";
    } else if (resource === "database-sync") {
      path = "/api/v1/database/sync";
    } else {
      path = "/api/v1/database/" + resource.slice(9);
    }
  } else if (resource === "sources") {
    path = "/api/v1/database/sources";
  } else if (resource.startsWith("discovery-")) {
    path = "/api/v1/discovery/" + resource.slice(10);
  } else if (resource.startsWith("intel-")) {
    path = "/api/v1/intel/" + resource.slice(6);
  } else if (resource.startsWith("scanner-")) {
    path = "/api/v1/scanner/" + resource.slice(8);
  } else if (resource.startsWith("cluster-")) {
    path = "/api/v1/cluster/" + resource.slice(8);
  } else if (resource === "scanner") {
    path = "/api/v1/scanner/status";
  } else if (resource === "cluster") {
    path = "/api/v1/cluster/nodes";
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
    for (const name of [
      "host",
      "page",
      "wordpress",
      "type",
      "url",
      "limit",
      "q",
      "signature",
      "field",
      "filename",
      "status_code",
      "scanned",
      "source",
      "source_id",
      "id",
      "action",
      "with_evidence",
      "extended",
      "scanner",
      "format",
    ]) {
      const val = request.nextUrl.searchParams.get(name);
      if (val !== null) url.searchParams.set(name, val);
    }
    const response = await fetch(url, {
      headers: { Authorization: "Bearer " + key },
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.any([request.signal, AbortSignal.timeout(60000)]),
    });
    const upstreamContentType = response.headers.get("content-type") || "application/json";
    const contentDisposition = response.headers.get("content-disposition");
    const headers: Record<string, string> = {
      "Content-Type": upstreamContentType,
      "Cache-Control": "no-store",
    };
    if (contentDisposition) {
      headers["Content-Disposition"] = contentDisposition;
    }
    return new Response(response.body, {
      status: response.status,
      headers,
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
    for (const name of ["url", "limit", "concurrency", "action"]) {
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
