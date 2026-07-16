"use client";

// Shell —— 全站客户端外壳：输码门 + 底部标签栏 + 全局 401 处理。
//
// 输码门：token 存 localStorage（家庭自用，够了）。首次打开输一次，
// 验证方式是拿它真打一个最轻的 API（/api/seasonal）——能 200 就是对的钥匙。
// 任何页面任何请求收到 401 都会广播 UNAUTHORIZED_EVENT，这里收到就清 token 回门口。
import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { getToken, setToken, clearToken, UNAUTHORIZED_EVENT } from "@/lib/api";

const TABS = [
  { href: "/", label: "聊天", icon: "💬" },
  { href: "/history", label: "历史", icon: "📖" },
];

export default function Shell({ children }: { children: React.ReactNode }) {
  // null = 还没读 localStorage（首帧），"" = 没有 token（进门），其余 = 已登录。
  const [token, setTok] = useState<string | null>(null);

  useEffect(() => {
    setTok(getToken());
    const onUnauthorized = () => {
      clearToken();
      setTok("");
    };
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, []);

  if (token === null) return null; // 读 localStorage 前不闪门
  if (token === "") return <TokenGate onPass={(t) => setTok(t)} />;

  return (
    <div className="flex h-full flex-col">
      <main className="min-h-0 flex-1">{children}</main>
      <TabBar />
    </div>
  );
}

function TabBar() {
  const pathname = usePathname();
  return (
    <nav className="flex shrink-0 border-t border-stone-200 bg-white pb-[env(safe-area-inset-bottom)]">
      {TABS.map((t) => {
        const active =
          t.href === "/" ? pathname === "/" : pathname.startsWith(t.href);
        return (
          <Link
            key={t.href}
            href={t.href}
            className={`flex flex-1 flex-col items-center gap-0.5 py-2 text-xs ${
              active ? "text-orange-600" : "text-stone-400"
            }`}
          >
            <span className="text-xl leading-none">{t.icon}</span>
            {t.label}
          </Link>
        );
      })}
    </nav>
  );
}

function TokenGate({ onPass }: { onPass: (t: string) => void }) {
  const [input, setInput] = useState("");
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");

  async function verify() {
    const t = input.trim();
    if (!t || checking) return;
    setChecking(true);
    setError("");
    try {
      const resp = await fetch("/api/seasonal", {
        headers: { Authorization: `Bearer ${t}` },
      });
      if (resp.status === 401) {
        setError("访问码不对，再核对一下");
        return;
      }
      if (!resp.ok) {
        setError(`服务器返回 ${resp.status}，稍后再试`);
        return;
      }
      setToken(t);
      onPass(t);
    } catch {
      setError("连不上服务器，检查网络后重试");
    } finally {
      setChecking(false);
    }
  }

  return (
    <div className="flex h-full flex-col items-center justify-center gap-5 px-8">
      <div className="text-5xl">🍅</div>
      <h1 className="text-xl font-bold">备餐助手</h1>
      <p className="text-center text-sm text-stone-500">
        输入家庭访问码开始使用（问一下部署这个服务的家人）
      </p>
      <input
        type="password"
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && verify()}
        placeholder="访问码"
        autoFocus
        className="w-full max-w-xs rounded-xl border border-stone-300 bg-white px-4 py-3 text-center outline-none focus:border-orange-400"
      />
      {error && <p className="text-sm text-red-500">{error}</p>}
      <button
        onClick={verify}
        disabled={checking || !input.trim()}
        className="w-full max-w-xs rounded-xl bg-orange-500 py-3 font-medium text-white transition active:scale-95 disabled:bg-stone-300"
      >
        {checking ? "验证中…" : "进入"}
      </button>
    </div>
  );
}
