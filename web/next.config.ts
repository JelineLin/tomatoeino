import type { NextConfig } from "next";

// 两副面孔（按运行阶段切换）：
//   - 开发（next dev）：把 /api/* 反向代理到本地 Go 服务——前端跑在 :3000、后端跑在 :8080，
//     浏览器看到的仍是同源，不碰 CORS。API_PROXY 可指到别处。
//   - 构建（next build）：output: "export" 纯静态导出到 web/out，由 Go 服务在同域名下托管
//     （cmd/server 的 spaHandler）。静态导出不支持 rewrites，所以代理只在 dev 阶段给。
// trailingSlash：导出成 /history/index.html 这种目录形态，Go 的 FileServer 直接就能伺服。
const nextConfig = (phase: string): NextConfig => {
  if (phase === "phase-development-server") {
    return {
      async rewrites() {
        const target = process.env.API_PROXY || "http://127.0.0.1:8080";
        return [{ source: "/api/:path*", destination: `${target}/api/:path*` }];
      },
    };
  }
  return {
    output: "export",
    trailingSlash: true,
    images: { unoptimized: true },
  };
};

export default nextConfig;
