"use client";

// 历史页：按天倒序列出三餐，每道菜可记「菜级反馈」。
// 行为对齐 iOS HistoryView：
//   - 反馈写入串行化（写链）：并发保存时旧快照的响应后到会盖掉新状态；
//   - 反馈失败走独立的 actionError 提示条，绝不顶掉整页列表（那是首载失败才有的待遇）。
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Day, type EditDish, type Feedback, type Meal } from "@/lib/api";
import MealRow from "@/components/MealRow";
import FeedbackSheet from "@/components/FeedbackSheet";
import MealEditorSheet from "@/components/MealEditorSheet";

const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

function weekdayName(date: string): string {
  const d = new Date(`${date}T00:00:00`);
  return isNaN(d.getTime()) ? "" : WEEKDAYS[d.getDay()];
}

interface EditingTarget {
  date: string;
  field: string; // lunch/fruit/dinner
  dish: string;
  current: Feedback | null;
}

// 正在编辑/补记的一餐（打开整餐编辑弹层）。meal 为 null = 这天该餐还没记录（补记）。
interface MealTarget {
  date: string;
  field: string;
  label: string;
  meal: Meal | null;
}

const FIELD_LABEL: [string, string][] = [
  ["lunch", "午餐"],
  ["fruit", "水果"],
  ["dinner", "晚餐"],
];

export default function HistoryPage() {
  const [days, setDays] = useState<Day[]>([]);
  const [loading, setLoading] = useState(true);
  const [fatalError, setFatalError] = useState("");   // 首载失败：整屏错误 + 重试
  const [actionError, setActionError] = useState(""); // 反馈失败：顶部提示条，列表不动
  const [editing, setEditing] = useState<EditingTarget | null>(null);
  const [mealEditing, setMealEditing] = useState<MealTarget | null>(null); // 整餐编辑/补记
  const [addPicking, setAddPicking] = useState(false); // 「记一餐」的日期/餐别选择步
  // 写链：后一笔写等前一笔落地（对齐 iOS 的 writeChain 串行化）。
  const writeChain = useRef<Promise<void>>(Promise.resolve());

  const load = useCallback(async () => {
    setFatalError("");
    try {
      setDays((await api.history()).reverse()); // 最近的排最前
    } catch (e) {
      // 已有数据时刷新失败静默保留旧列表（和 iOS 同口径）。
      setDays((prev) => {
        if (prev.length === 0) setFatalError(e instanceof Error ? e.message : String(e));
        return prev;
      });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  function submitFeedback(t: EditingTarget, rating: string, note: string) {
    writeChain.current = writeChain.current.then(async () => {
      try {
        setDays((await api.submitFeedback(t.date, t.field, t.dish, rating, note)).reverse());
        setActionError("");
      } catch (e) {
        setActionError(`「${t.dish}」的反馈没保存成功：${e instanceof Error ? e.message : e}`);
      }
    });
  }

  // 整餐保存（编辑已有 / 补记缺席的一餐）：走 /api/history/apply 的整餐覆盖，
  // 菜级反馈由后端按菜名结转——改做法不丢已记的反馈。
  function saveMeal(t: MealTarget, time: string, dishes: EditDish[]) {
    writeChain.current = writeChain.current.then(async () => {
      try {
        setDays((await api.applyMeal(t.date, t.field, time, dishes)).reverse());
        setActionError("");
      } catch (e) {
        setActionError(`${t.date} ${t.label}没保存成功：${e instanceof Error ? e.message : e}`);
      }
    });
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-stone-200 bg-white px-4 py-3">
        <span className="font-semibold">吃饭历史</span>
        <div className="flex gap-3 text-sm">
          <button onClick={() => setAddPicking(true)} className="text-orange-500 active:scale-95">
            ＋ 记一餐
          </button>
          <a href="/history/import/" className="text-orange-500 active:scale-95">⤓ 导入</a>
          <button onClick={load} className="text-orange-500 active:scale-95" aria-label="刷新">
            ↻ 刷新
          </button>
        </div>
      </header>

      {actionError && (
        <div className="flex items-center justify-between bg-red-50 px-4 py-2 text-sm text-red-600">
          <span className="min-w-0 flex-1">{actionError}</span>
          <button onClick={() => setActionError("")} className="ml-2 shrink-0">✕</button>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {loading ? (
          <p className="pt-16 text-center text-sm text-stone-400">加载中…</p>
        ) : fatalError ? (
          <div className="flex flex-col items-center gap-3 pt-16">
            <div className="text-3xl">⚠️</div>
            <p className="text-sm text-stone-500">{fatalError}</p>
            <button onClick={load} className="rounded-full bg-orange-500 px-5 py-2 text-sm text-white active:scale-95">
              重试
            </button>
          </div>
        ) : days.length === 0 ? (
          <div className="flex flex-col items-center gap-2 pt-16 text-center">
            <div className="text-3xl">📭</div>
            <p className="text-sm text-stone-500">还没有记录——在聊天里说「今天中午吃了…」就会记下来</p>
          </div>
        ) : (
          <div className="space-y-3">
            {days.map((day) => (
              <section key={day.date} className="rounded-2xl bg-white p-3.5 shadow-sm">
                <div className="mb-2 flex items-baseline gap-1.5">
                  <span className="text-sm font-bold">{day.date}</span>
                  <span className="text-xs text-stone-400">{weekdayName(day.date)}</span>
                </div>
                <div className="space-y-2.5">
                  {FIELD_LABEL.map(([field, label]) => {
                    const meal = day[field as "lunch" | "fruit" | "dinner"];
                    return (
                      <MealRow
                        key={field}
                        label={label}
                        meal={meal}
                        onFeedback={(dish, current) =>
                          setEditing({ date: day.date, field, dish, current })
                        }
                        onEdit={() =>
                          setMealEditing({ date: day.date, field, label, meal: meal ?? null })
                        }
                      />
                    );
                  })}
                  {/* 缺席的餐给「补记」小入口：漏记的那顿随手补上 */}
                  <div className="flex gap-1.5">
                    {FIELD_LABEL.filter(
                      ([f]) => !day[f as "lunch" | "fruit" | "dinner"]?.dishes?.length,
                    ).map(([field, label]) => (
                      <button
                        key={field}
                        onClick={() => setMealEditing({ date: day.date, field, label, meal: null })}
                        className="rounded-full bg-stone-100 px-2.5 py-1 text-xs text-stone-500 active:scale-95"
                      >
                        ＋ 补记{label}
                      </button>
                    ))}
                  </div>
                </div>
              </section>
            ))}
          </div>
        )}
      </div>

      {editing && (
        <FeedbackSheet
          dishName={editing.dish}
          current={editing.current}
          onSave={(rating, note) => submitFeedback(editing, rating, note)}
          onClose={() => setEditing(null)}
        />
      )}

      {mealEditing && (
        <MealEditorSheet
          title={`${mealEditing.meal ? "编辑" : "补记"} ${mealEditing.date} ${mealEditing.label}`}
          confirmLabel="保存"
          initialTime={mealEditing.meal?.time ?? ""}
          initialDishes={
            mealEditing.meal?.dishes.map((d) => ({ name: d.name, detail: d.detail })) ?? []
          }
          onSave={(time, dishes) => saveMeal(mealEditing, time, dishes)}
          onClose={() => setMealEditing(null)}
        />
      )}

      {addPicking && (
        <AddMealPicker
          onPick={(date, field, label) => {
            setAddPicking(false);
            // 那天那餐已有记录就带出来编辑，没有就是补记。
            const existing = days.find((d) => d.date === date)?.[field as "lunch" | "fruit" | "dinner"];
            setMealEditing({ date, field, label, meal: existing ?? null });
          }}
          onClose={() => setAddPicking(false)}
        />
      )}
    </div>
  );
}

// 「记一餐」的第一步：选日期 + 餐别，然后进整餐编辑弹层。
// 独立成一步是因为日期/餐别是写入的主键，改错会覆盖别的记录——让家长先明确写到哪。
function AddMealPicker({
  onPick,
  onClose,
}: {
  onPick: (date: string, field: string, label: string) => void;
  onClose: () => void;
}) {
  const [date, setDate] = useState(() => {
    const d = new Date();
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  });

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/30" onClick={onClose}>
      <div
        className="w-full max-w-lg rounded-t-2xl bg-white p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 text-center font-semibold">记一餐到哪天？</div>
        <input
          type="date"
          value={date}
          onChange={(e) => setDate(e.target.value)}
          className="w-full rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
        />
        <div className="mt-3 flex gap-2">
          {FIELD_LABEL.map(([field, label]) => (
            <button
              key={field}
              onClick={() => date && onPick(date, field, label)}
              disabled={!date}
              className="flex-1 rounded-xl bg-orange-50 py-2.5 text-sm text-orange-600 active:scale-95 disabled:opacity-40"
            >
              {label}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
