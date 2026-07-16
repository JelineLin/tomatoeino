"use client";

// 历史页：按天倒序列出三餐，每道菜可记「菜级反馈」。
// 行为对齐 iOS HistoryView：
//   - 反馈写入串行化（写链）：并发保存时旧快照的响应后到会盖掉新状态；
//   - 反馈失败走独立的 actionError 提示条，绝不顶掉整页列表（那是首载失败才有的待遇）。
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Day, type Feedback } from "@/lib/api";
import MealRow from "@/components/MealRow";
import FeedbackSheet from "@/components/FeedbackSheet";

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

export default function HistoryPage() {
  const [days, setDays] = useState<Day[]>([]);
  const [loading, setLoading] = useState(true);
  const [fatalError, setFatalError] = useState("");   // 首载失败：整屏错误 + 重试
  const [actionError, setActionError] = useState(""); // 反馈失败：顶部提示条，列表不动
  const [editing, setEditing] = useState<EditingTarget | null>(null);
  // 写链：后一笔反馈等前一笔落地（对齐 iOS 的 writeChain 串行化）。
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

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-stone-200 bg-white px-4 py-3">
        <span className="font-semibold">吃饭历史</span>
        <button onClick={load} className="text-sm text-orange-500 active:scale-95" aria-label="刷新">
          ↻ 刷新
        </button>
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
                  {(
                    [
                      ["午餐", "lunch", day.lunch],
                      ["水果", "fruit", day.fruit],
                      ["晚餐", "dinner", day.dinner],
                    ] as const
                  ).map(([label, field, meal]) => (
                    <MealRow
                      key={field}
                      label={label}
                      meal={meal}
                      onFeedback={(dish, current) =>
                        setEditing({ date: day.date, field, dish, current })
                      }
                    />
                  ))}
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
    </div>
  );
}
