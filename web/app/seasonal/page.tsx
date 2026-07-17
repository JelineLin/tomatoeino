"use client";

// 时令页：某个月的应季食材（蔬菜/水果/水产）+ 备餐提示 + 数据来源。
// 数据和 agent 的 seasonal_produce 工具同一张表——聊天里说的应季和这里展示的永远一个口径。
// 默认当前月（几月由后端说了算），左右箭头环形翻页（12 月再往右回到 1 月）。
import { useCallback, useEffect, useState } from "react";
import { api, type Season } from "@/lib/api";

function theme(month: number): { emoji: string; name: string; grad: string } {
  if (month >= 3 && month <= 5) return { emoji: "🌸", name: "春", grad: "from-green-500 to-lime-500" };
  if (month >= 6 && month <= 8) return { emoji: "☀️", name: "夏", grad: "from-cyan-600 to-teal-500" };
  if (month >= 9 && month <= 11) return { emoji: "🍂", name: "秋", grad: "from-orange-500 to-amber-500" };
  return { emoji: "❄️", name: "冬", grad: "from-blue-500 to-indigo-400" };
}

export default function SeasonalPage() {
  const [season, setSeason] = useState<Season | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async (month?: number) => {
    setError("");
    try {
      setSeason(await api.seasonal(month));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  if (error && !season) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3">
        <p className="text-sm text-stone-500">{error}</p>
        <button onClick={() => load()} className="rounded-full bg-orange-500 px-5 py-2 text-sm text-white active:scale-95">
          重试
        </button>
      </div>
    );
  }
  if (!season) return <p className="pt-16 text-center text-sm text-stone-400">加载中…</p>;

  const t = theme(season.month);
  return (
    <div className="h-full overflow-y-auto px-3 py-3">
      <div className="space-y-3">
        <div className={`flex items-center justify-between rounded-2xl bg-gradient-to-br ${t.grad} px-4 py-5 text-white`}>
          <button
            onClick={() => load(season.month === 1 ? 12 : season.month - 1)}
            className="flex h-9 w-9 items-center justify-center rounded-full bg-white/20 active:scale-90"
            aria-label="上个月"
          >
            ‹
          </button>
          <div className="text-center">
            <div className="text-2xl font-bold">{t.emoji} {season.month}月</div>
            <div className="text-sm opacity-85">{t.name}季时令</div>
          </div>
          <button
            onClick={() => load(season.month === 12 ? 1 : season.month + 1)}
            className="flex h-9 w-9 items-center justify-center rounded-full bg-white/20 active:scale-90"
            aria-label="下个月"
          >
            ›
          </button>
        </div>

        <CategoryCard title="应季蔬菜" icon="🥬" items={season.veg} chip="bg-green-50 text-green-700" />
        <CategoryCard title="应季水果" icon="🍎" items={season.fruit} chip="bg-pink-50 text-pink-700" />
        <CategoryCard title="应季水产" icon="🐟" items={season.aquatic} chip="bg-blue-50 text-blue-700" />

        <div className="rounded-2xl bg-yellow-50 p-3.5 text-sm text-stone-600">💡 {season.tip}</div>

        {season.source && (
          <div className="rounded-2xl bg-stone-100 p-3.5">
            <div className="text-xs font-semibold text-stone-500">ⓘ 数据来源</div>
            <div className="mt-0.5 text-xs text-stone-500">{season.source}</div>
          </div>
        )}
      </div>
    </div>
  );
}

function CategoryCard({ title, icon, items, chip }: { title: string; icon: string; items: string[]; chip: string }) {
  return (
    <div className="rounded-2xl bg-white p-3.5 shadow-sm">
      <div className="mb-2 flex items-baseline justify-between">
        <span className="text-sm font-semibold">{icon} {title}</span>
        <span className="text-xs text-stone-400">{items.length} 种</span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {items.map((it) => (
          <span key={it} className={`rounded-full px-3 py-1 text-sm ${chip}`}>{it}</span>
        ))}
      </div>
    </div>
  );
}
