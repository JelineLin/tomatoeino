import type { NextConfig } from "next";

const nextConfig = (phase: string): NextConfig => {
  if (phase === "phase-development-server") {
    return {
      async rewrites() {
        const target = process.env.API_PROXY || "http://127.0.0.1:8450";
        return [{ source: "/api/:path*", destination: `${target}/api/:path*` }];
      },
    };
  }
  return { output: "export", trailingSlash: true, images: { unoptimized: true } };
};

export default nextConfig;
