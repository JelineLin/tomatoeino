"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { api, clearToken, getToken, setToken, NAVIGATION_EVENT, type NavigationPosition, UNAUTHORIZED_EVENT } from "@/lib/api";

const tabs = [
  { href: "/", icon: "📘", label: "今日" }, { href: "/record", icon: "🎙️", label: "朗读" },
  { href: "/history", icon: "🗓️", label: "历史" }, { href: "/progress", icon: "📈", label: "进度" },
  { href: "/reports", icon: "📝", label: "周报" }, { href: "/profile", icon: "👤", label: "档案" },
];

export default function Shell({ children }: { children: React.ReactNode }) {
  const [token, setCurrent] = useState<string | null>(null);
  const [position, setPosition] = useState<NavigationPosition>("bottom");
  useEffect(() => { const initial = window.setTimeout(() => setCurrent(getToken()), 0); const reset = () => { clearToken(); setCurrent("") }; window.addEventListener(UNAUTHORIZED_EVENT, reset); return () => { window.clearTimeout(initial); window.removeEventListener(UNAUTHORIZED_EVENT, reset) } }, []);
  useEffect(() => { if (!token) return; let active = true; const change = (event: Event) => { const next = (event as CustomEvent<NavigationPosition>).detail; if (next === "bottom" || next === "left" || next === "right") setPosition(next) }; window.addEventListener(NAVIGATION_EVENT, change); api.profile().then(profile => { if (active) setPosition(profile.navigation_position || "bottom") }).catch(() => {}); return () => { active = false; window.removeEventListener(NAVIGATION_EVENT, change) } }, [token]);
  if (token === null) return null;
  if (!token) return <TokenGate onPass={setCurrent} />;
  if (position === "bottom") return <div className="mx-auto flex h-full max-w-2xl flex-col bg-[#f5f7ff] shadow-sm"><main className="min-h-0 flex-1 overflow-y-auto">{children}</main><TabBar position="bottom" /></div>;
  return <div className="mx-auto flex h-full max-w-5xl flex-row bg-[#f5f7ff] shadow-sm">{position === "left" && <TabBar position="left" />}<main className="min-h-0 min-w-0 flex-1 overflow-y-auto">{children}</main>{position === "right" && <TabBar position="right" />}</div>;
}

function TabBar({ position }: { position: NavigationPosition }) {
  const path = usePathname();
  const side = position !== "bottom";
  return <nav className={side ? `flex w-24 shrink-0 flex-col gap-1 bg-white px-2 py-4 ${position === "left" ? "border-r" : "border-l"} border-indigo-100` : "grid shrink-0 grid-cols-6 border-t border-indigo-100 bg-white pb-[env(safe-area-inset-bottom)]"}>{tabs.map(t => { const active = t.href === "/" ? path === "/" : path.startsWith(t.href); return <Link key={t.href} href={t.href} className={`${side ? "flex items-center gap-2 rounded-xl px-2 py-3 text-xs" : "flex flex-col items-center gap-0.5 py-2 text-[10px]"} ${active ? "bg-indigo-50 text-indigo-600" : "text-slate-400"}`}><span className={side ? "text-xl" : "text-lg"}>{t.icon}</span>{t.label}</Link> })}</nav>;
}

function TokenGate({ onPass }: { onPass: (token: string) => void }) {
  const [value, setValue] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  async function enter() { const token = value.trim(); if (!token) return; setBusy(true); setError(""); try { const response = await fetch("/api/english/profile", { headers: { Authorization: `Bearer ${token}` } }); if (response.status === 401) { setError("访问码不正确"); return } if (!response.ok) { setError("服务暂时不可用"); return } setToken(token); onPass(token) } catch { setError("无法连接 English Coach") } finally { setBusy(false) } }
  return <div className="flex h-full flex-col items-center justify-center bg-gradient-to-b from-indigo-50 to-white px-8"><div className="mb-5 text-6xl">📘</div><h1 className="text-2xl font-bold text-indigo-950">English Coach</h1><p className="mt-2 text-center text-sm text-slate-500">每天 30～60 分钟，稳步练阅读与口语</p><input type="password" value={value} onChange={e => setValue(e.target.value)} onKeyDown={e => e.key === "Enter" && enter()} placeholder="独立访问码" autoFocus className="mt-8 w-full max-w-xs rounded-2xl border border-indigo-200 bg-white px-4 py-3 text-center outline-none focus:border-indigo-500" />{error && <p className="mt-3 text-sm text-red-500">{error}</p>}<button onClick={enter} disabled={busy || !value.trim()} className="mt-4 w-full max-w-xs rounded-2xl bg-indigo-600 py-3 font-semibold text-white disabled:bg-slate-300">{busy ? "验证中…" : "开始学习"}</button></div>;
}
