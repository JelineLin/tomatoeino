// 根布局（服务端组件）：只负责 HTML 外壳和元数据；
// 交互（输码门 / 标签栏 / 401 处理）都在客户端的 Shell 里。
// 不用 Google Fonts（next/font 构建时要联网拉字体，国内网络不稳）——中文站直接系统字体栈。
import type { Metadata, Viewport } from "next";
import "./globals.css";
import Shell from "@/components/Shell";

export const metadata: Metadata = {
  title: "备餐助手",
  description: "幼儿备餐 agent 网页版",
  // PWA：manifest + 苹果主屏图标——Safari「添加到主屏幕」后就是一个全屏 App。
  manifest: "/manifest.webmanifest",
  icons: { apple: "/apple-touch-icon.png" },
  appleWebApp: { capable: true, title: "备餐助手", statusBarStyle: "default" },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 1, // 移动端表单聚焦不放大，贴近原生手感
  themeColor: "#f97316",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" className="h-full">
      <body className="h-full bg-stone-50 text-stone-900 antialiased">
        <Shell>{children}</Shell>
      </body>
    </html>
  );
}
