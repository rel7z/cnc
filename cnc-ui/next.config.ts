import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  // A batch of up to 20 provider requests can exceed the default 30-second proxy timeout.
  experimental: { proxyTimeout: 20 * 60 * 1000, proxyClientMaxBodySize: "96mb" },
  async rewrites() {
    const apiUrl = process.env.GO_API_URL || "http://localhost:8080";
    return { fallback: [
      {
        // Dashboard route handlers, including dynamic template routes, take priority.
        source: "/api/:path*",
        destination: `${apiUrl}/api/:path*`,
        missing: [{ type: "header", key: "x-skip-rewrite" }],
      },
    ] };
  },
};

export default nextConfig;
