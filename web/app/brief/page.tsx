"use client";

// 今日推荐页：后端定时生成的备餐简报 + 结构化菜单卡片。
// 行为对齐 iOS BriefView：
//   - 卡片可「编辑并采纳」：改时间/菜品后写入历史；采纳成功把编辑版写回卡片
//     并标已采纳（后端简报缓存同步回写了，重进恢复也一致）；
//   - 「复制」把整份推荐排成纯文本清单进剪贴板；
//   - 简报不是今天的给一条过期提示；还没有简报给「立即生成」。
import { useCallback, useEffect, useState } from "react";
import Markdown from "react-markdown";
import { api, ApiError, type DailyBrief, type EditDish, type ProposedMeal } from "@/lib/api";
import MealEditorSheet from "@/components/MealEditorSheet";

const MEAL_LABEL: Record<string, string> = { lunch: "午餐", fruit: "水果", dinner: "晚餐" };
const MEAL_ICON: Record<string, string> = { lunch: "🍚", fruit: "🍎", dinner: "🍲" };

function todayStr(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export default function BriefPage() {
  const [brief, setBrief] = useState<DailyBrief | null>(null);
  const [notReady, setNotReady] = useState(false); // 404：还没有简报
  const [loading, setLoading] = useState(true);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState("");
  const [applyError, setApplyError] = useState("");
  const [applied, setApplied] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<ProposedMeal | null>(null);
  const [copied, setCopied] = useState(false);

  const restoreApplied = (b: DailyBrief | null) =>
    setApplied(new Set(b?.menu?.meals.filter((m) => m.applied).map((m) => m.meal) ?? []));

  const load = useCallback(async (refresh: boolean) => {
    if (refresh) setGenerating(true);
    setError("");
    setNotReady(false);
    try {
      const b = await api.brief(refresh);
      setBrief(b);
      restoreApplied(b);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setNotReady(true);
      else setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
      setGenerating(false);
    }
  }, []);

  useEffect(() => { load(false); }, [load]);

  async function applyMeal(meal: ProposedMeal, time: string, dishes: EditDish[]) {
    setApplyError("");
    try {
      await api.applyMeal(brief!.menu!.date, meal.meal, time, dishes);
      // 本地写回：卡片显示的必须是实际采纳的版本（后端简报缓存也回写了）。
      setBrief((prev) => {
        if (!prev?.menu) return prev;
        return {
          ...prev,
          menu: {
            ...prev.menu,
            meals: prev.menu.meals.map((m) =>
              m.meal === meal.meal ? { ...m, time, dishes, applied: true } : m,
            ),
          },
        };
      });
      setApplied((prev) => new Set(prev).add(meal.meal));
    } catch (e) {
      setApplyError(`采纳没成功：${e instanceof Error ? e.message : e}`);
    }
  }

  function copyMenu() {
    const menu = brief?.menu;
    if (!menu) return;
    const lines = [`${menu.date} 推荐菜单`];
    for (const m of menu.meals) {
      lines.push(`${MEAL_ICON[m.meal] ?? "🍽"} ${MEAL_LABEL[m.meal] ?? m.meal}${m.time ? `（${m.time}）` : ""}`);
      for (const d of m.dishes) lines.push(d.detail ? `- ${d.name}：${d.detail}` : `- ${d.name}`);
    }
    navigator.clipboard.writeText(lines.join("\n")).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-stone-200 bg-white px-4 py-3">
        <span className="font-semibold">今日推荐</span>
        <button
          onClick={() => load(true)}
          disabled={generating}
          className="text-sm text-orange-500 active:scale-95 disabled:text-stone-300"
        >
          {generating ? "生成中…" : "↻ 重新生成"}
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {loading ? (
          <p className="pt-16 text-center text-sm text-stone-400">加载中…</p>
        ) : notReady && !brief ? (
          <div className="flex flex-col items-center gap-3 pt-16 text-center">
            <div className="text-4xl">📋</div>
            <p className="text-sm text-stone-500">今天的简报还没生成（每天早上 7 点自动出）</p>
            <button
              onClick={() => load(true)}
              disabled={generating}
              className="rounded-full bg-orange-500 px-5 py-2 text-sm text-white active:scale-95 disabled:bg-stone-300"
            >
              {generating ? "生成中（要跑一会儿）…" : "立即生成"}
            </button>
          </div>
        ) : error && !brief ? (
          <div className="flex flex-col items-center gap-3 pt-16">
            <p className="text-sm text-stone-500">{error}</p>
            <button onClick={() => load(false)} className="rounded-full bg-orange-500 px-5 py-2 text-sm text-white active:scale-95">
              重试
            </button>
          </div>
        ) : brief ? (
          <div className="space-y-3">
            <div className="rounded-2xl bg-gradient-to-br from-orange-500 to-orange-600 p-4 text-white">
              <div className="flex items-baseline justify-between">
                <span className="text-lg font-bold">📋 {brief.date}</span>
                <span className="text-xs opacity-85">
                  {new Date(brief.generatedAt).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })} 生成
                </span>
              </div>
              {brief.date !== todayStr() && (
                <div className="mt-1.5 rounded-lg bg-white/20 px-2.5 py-1 text-xs">
                  这是 {brief.date} 的简报，点右上角可为今天重新生成
                </div>
              )}
            </div>

            {applyError && (
              <div className="flex items-center justify-between rounded-xl bg-red-50 px-3.5 py-2 text-sm text-red-600">
                <span>{applyError}</span>
                <button onClick={() => setApplyError("")}>✕</button>
              </div>
            )}

            {brief.menu && brief.menu.meals.length > 0 && (
              <section>
                <div className="mb-2 flex items-center justify-between">
                  <span className="font-semibold">推荐菜单 · 可编辑后采纳</span>
                  <button
                    onClick={copyMenu}
                    disabled={copied}
                    className="rounded-full border border-stone-300 px-3 py-1 text-xs text-stone-600 active:scale-95"
                  >
                    {copied ? "✓ 已复制" : "⧉ 复制"}
                  </button>
                </div>
                <div className="space-y-2.5">
                  {brief.menu.meals.map((m) => (
                    <MealCard
                      key={m.meal}
                      meal={m}
                      applied={applied.has(m.meal)}
                      onEdit={() => setEditing(m)}
                    />
                  ))}
                </div>
              </section>
            )}

            <section className="rounded-2xl bg-white p-4 shadow-sm">
              <div className="text-[15px] leading-relaxed [&_h1]:text-base [&_h1]:font-bold [&_h2]:text-base [&_h2]:font-bold [&_h3]:font-semibold [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-1 [&_strong]:font-semibold [&_ul]:list-disc [&_ul]:pl-5">
                <Markdown>{brief.content}</Markdown>
              </div>
            </section>
          </div>
        ) : null}
      </div>

      {editing && brief?.menu && (
        <MealEditorSheet
          title={`编辑${MEAL_LABEL[editing.meal] ?? editing.meal}`}
          confirmLabel="采纳并写入历史"
          initialTime={editing.time}
          initialDishes={editing.dishes}
          onSave={(time, dishes) => applyMeal(editing, time, dishes)}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}

function MealCard({ meal, applied, onEdit }: { meal: ProposedMeal; applied: boolean; onEdit: () => void }) {
  return (
    <div className="rounded-2xl bg-white p-3.5 shadow-sm">
      <div className="flex items-center gap-1.5">
        <span>{MEAL_ICON[meal.meal] ?? "🍽"}</span>
        <span className="text-sm font-semibold">{MEAL_LABEL[meal.meal] ?? meal.meal}</span>
        {meal.time && <span className="text-xs tabular-nums text-stone-400">{meal.time}</span>}
        <span className="flex-1" />
        {applied ? (
          <span className="rounded-full bg-green-50 px-2.5 py-1 text-xs text-green-600">✓ 已采纳</span>
        ) : (
          <button
            onClick={onEdit}
            className="rounded-full border border-orange-300 px-2.5 py-1 text-xs text-orange-600 active:scale-95"
          >
            ✎ 编辑并采纳
          </button>
        )}
      </div>
      <div className="mt-2 space-y-1.5">
        {meal.dishes.map((d, i) => (
          <div key={i}>
            <div className="text-[15px]">{d.name}</div>
            {d.detail && <div className="text-xs text-stone-500">{d.detail}</div>}
          </div>
        ))}
      </div>
      {meal.reason && <div className="mt-2 text-xs text-stone-400">💡 {meal.reason}</div>}
    </div>
  );
}

