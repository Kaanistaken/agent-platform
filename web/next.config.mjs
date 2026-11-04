// The browser talks only to Next.js; /api/* is forwarded to the Go backend,
// so cookies and the Origin check work on one origin without CORS (D68).
const backend = process.env.BACKEND_URL || "http://127.0.0.1:8080";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Compression would buffer the SSE stream (/api/runs/{id}/events).
  compress: false,
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
  },
};

export default nextConfig;
