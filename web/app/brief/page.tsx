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

// 份数渲染：整数不带小数点（2），半份保留 0.5。和后端 fmtQty 一个口径。
const fmtQty = (q: number) => String(q);

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
  const [consumedNotice, setConsumedNotice] = useState(""); // 采纳后自动扣了哪些库存
  const [replacing, setReplacing] = useState(""); // "lunch-0" 这种键：哪道菜正在重算
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
    setConsumedNotice("");
    try {
      const result = await api.applyMeal(brief!.menu!.date, meal.meal, time, dishes);
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
      // 出库是家长没点过的副作用，不明说等于偷改账本。
      const consumed = result.consumed ?? [];
      if (consumed.length > 0) {
        setConsumedNotice(
          "已扣减库存：" +
            consumed
              .map((c) => `${c.name} ${fmtQty(c.qty)}${c.unit}${c.depleted ? "（用完了）" : ""}`)
              .join("、"),
        );
      }
    } catch (e) {
      setApplyError(`采纳没成功：${e instanceof Error ? e.message : e}`);
    }
  }

  // replaceDish 让 agent 只重算这一道菜（备选都不满意时的兜底）。
  // 比整份重生成快得多，但仍要等几十秒，所以要给明确的进行中反馈。
  async function replaceDish(field: string, dishIndex: number) {
    const key = `${field}-${dishIndex}`;
    if (replacing) return;
    setReplacing(key);
    setApplyError("");
    try {
      const result = await api.replaceDish(field, dishIndex);
      // 服务端已经把简报缓存改好了，直接用它回的整份菜单替换本地状态，两边不会漂。
      if (result.menu) {
        setBrief((prev) => (prev ? { ...prev, menu: result.menu } : prev));
      }
      setConsumedNotice(result.note || `已换成「${result.dish.name}」`);
    } catch (e) {
      setApplyError(`换菜没成功：${e instanceof Error ? e.message : e}`);
    } finally {
      setReplacing("");
    }
  }

  // swapAlternative 把主推的第 di 道菜和第 ai 道备选【对调】。
  //
  // 「调整菜单」的主路径：纯本地状态交换，不发请求、不跑 agent、不花 token。
  // 对调而不是覆盖——换走的那道回到备选位，家长反悔能换回来。
  // uses 跟着整个 EditDish 走，采纳时扣的自然是换之后那道菜的食材。
  function swapAlternative(field: string, di: number, ai: number) {
    setBrief((prev) => {
      if (!prev?.menu) return prev;
      return {
        ...prev,
        menu: {
          ...prev.menu,
          meals: prev.menu.meals.map((m) => {
            if (m.meal !== field) return m;
            const alts = m.alternatives ?? [];
            if (!m.dishes[di] || !alts[ai]) return m;
            const dishes = [...m.dishes];
            const nextAlts = [...alts];
            [dishes[di], nextAlts[ai]] = [nextAlts[ai], dishes[di]];
            return { ...m, dishes, alternatives: nextAlts };
          }),
        },
      };
    });
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

            {/* 采纳后自动扣了库存——明示，不打断操作但也绝不隐瞒。 */}
            {consumedNotice && (
              <div className="flex items-center justify-between rounded-xl bg-green-50 px-3.5 py-2 text-sm text-green-700">
                <span>{consumedNotice}</span>
                <button onClick={() => setConsumedNotice("")}>✕</button>
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
                      replacing={replacing}
                      onEdit={() => setEditing(m)}
                      onSwap={(di, ai) => swapAlternative(m.meal, di, ai)}
                      onReplace={(di) => replaceDish(m.meal, di)}
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

function MealCard({
  meal,
  applied,
  replacing,
  onEdit,
  onSwap,
  onReplace,
}: {
  meal: ProposedMeal;
  applied: boolean;
  replacing: string;
  onEdit: () => void;
  onSwap: (dishIndex: number, altIndex: number) => void;
  onReplace: (dishIndex: number) => void;
}) {
  const alts = meal.alternatives ?? [];
  // 已采纳的餐不再提供换菜（账已经记了）。没备选也照样显示入口——
  // 「让 agent 另想一道」这条兜底路始终可用。
  const canSwap = !applied;
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
          <div key={i} className="flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <div className="text-[15px]">{d.name}</div>
              {d.detail && <div className="text-xs text-stone-500">{d.detail}</div>}
            </div>
            {/* 换菜两级：先给 agent 生成时顺手带回的备选（零请求、零延迟），
                都不满意再走「另想一道」——只重算这一道，不动整餐。 */}
            {canSwap &&
              (replacing === `${meal.meal}-${i}` ? (
                <span className="shrink-0 px-2 py-0.5 text-xs text-stone-400">重算中…</span>
              ) : (
                <details className="relative shrink-0">
                  <summary className="cursor-pointer list-none rounded-full border border-stone-200 px-2 py-0.5 text-xs text-stone-500 active:scale-95">
                    ⇄ 换
                  </summary>
                  <div className="absolute right-0 z-10 mt-1 w-52 overflow-hidden rounded-xl border border-stone-200 bg-white shadow-lg">
                    {alts.map((a, ai) => (
                      <button
                        key={ai}
                        onClick={(e) => {
                          // 选完把 <details> 收起来，不然菜单一直挂在那儿。
                          (e.currentTarget.closest("details") as HTMLDetailsElement | null)?.removeAttribute("open");
                          onSwap(i, ai);
                        }}
                        className="block w-full px-3 py-2 text-left text-xs hover:bg-stone-50"
                      >
                        <span className="text-stone-800">{a.name}</span>
                        {a.detail && <span className="text-stone-400">（{a.detail}）</span>}
                      </button>
                    ))}
                    <button
                      disabled={replacing !== ""}
                      onClick={(e) => {
                        (e.currentTarget.closest("details") as HTMLDetailsElement | null)?.removeAttribute("open");
                        onReplace(i);
                      }}
                      className="block w-full border-t border-stone-100 px-3 py-2 text-left text-xs text-orange-600 hover:bg-stone-50 disabled:opacity-50"
                    >
                      ✨ 让 agent 另想一道
                    </button>
                  </div>
                </details>
              ))}
          </div>
        ))}
      </div>
      {meal.reason && <div className="mt-2 text-xs text-stone-400">💡 {meal.reason}</div>}
    </div>
  );
}

