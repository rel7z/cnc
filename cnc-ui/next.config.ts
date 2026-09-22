import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  async rewrites() {
    const apiUrl = process.env.GO_API_URL || "http://localhost:8080";
    return [
      {
        // Proxy all API calls except /api/events (handled by the streaming route handler)
        source: "/api/:path*",
        destination: `${apiUrl}/api/:path*`,
        missing: [{ type: "header", key: "x-skip-rewrite" }],
      },
    ];
  },
};

export default nextConfig;
