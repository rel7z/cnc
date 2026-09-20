import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  async rewrites() {
    return [
      {
        // Proxy all API calls except /api/events (handled by the streaming route handler)
        source: "/api/:path*",
        destination: "http://localhost:8080/api/:path*",
        missing: [{ type: "header", key: "x-skip-rewrite" }],
      },
    ];
  },
};

export default nextConfig;
