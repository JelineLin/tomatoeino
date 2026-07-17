"use client";

// MealEditorSheet —— 「编辑一餐」的通用底部弹层：时间 + 菜品增删改。
// 两个用户共用（写入语义都是 /api/history/apply 的整餐覆盖）：
//   - 推荐页：编辑推荐后「采纳并写入历史」；
//   - 历史页：改已有的餐 / 给某天补记一餐。
// 菜级反馈不在这里编辑——整餐覆盖时后端按菜名自动结转，改做法不丢反馈。
import { useState } from "react";
import type { EditDish } from "@/lib/api";

export default function MealEditorSheet({
  title,
  confirmLabel,
  initialTime,
  initialDishes,
  onSave,
  onClose,
}: {
  title: string;
  confirmLabel: string;
  initialTime: string;
  initialDishes: EditDish[];
  onSave: (time: string, dishes: EditDish[]) => void;
  onClose: () => void;
}) {
  const [time, setTime] = useState(initialTime);
  const [dishes, setDishes] = useState<EditDish[]>(
    initialDishes.length > 0 ? initialDishes.map((d) => ({ ...d })) : [{ name: "", detail: "" }],
  );

  const cleaned = dishes
    .map((d) => ({ name: d.name.trim(), detail: d.detail.trim() }))
    .filter((d) => d.name !== "");

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/30" onClick={onClose}>
      <div
        className="max-h-[85dvh] w-full max-w-lg overflow-y-auto rounded-t-2xl bg-white p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 text-center font-semibold">{title}</div>

        <label className="mb-1 block text-xs text-stone-500">时间（可留空）</label>
        <input
          value={time}
          onChange={(e) => setTime(e.target.value)}
          placeholder="如 12:00"
          className="mb-3 w-full rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
        />

        <label className="mb-1 block text-xs text-stone-500">菜品（可增删改）</label>
        <div className="space-y-2">
          {dishes.map((d, i) => (
            <div key={i} className="flex items-start gap-2 rounded-xl bg-stone-50 p-2.5">
              <div className="min-w-0 flex-1 space-y-1.5">
                <input
                  value={d.name}
                  onChange={(e) =>
                    setDishes((prev) => prev.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))
                  }
                  placeholder="菜名"
                  className="w-full rounded-lg bg-white px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-orange-300"
                />
                <input
                  value={d.detail}
                  onChange={(e) =>
                    setDishes((prev) => prev.map((x, j) => (j === i ? { ...x, detail: e.target.value } : x)))
                  }
                  placeholder="做法 / 分量（可留空）"
                  className="w-full rounded-lg bg-white px-3 py-2 text-xs outline-none focus:ring-1 focus:ring-orange-300"
                />
              </div>
              <button
                onClick={() => setDishes((prev) => prev.filter((_, j) => j !== i))}
                className="mt-1 shrink-0 text-stone-400 active:scale-95"
                aria-label="删除这道菜"
              >
                ✕
              </button>
            </div>
          ))}
        </div>
        <button
          onClick={() => setDishes((prev) => [...prev, { name: "", detail: "" }])}
          className="mt-2 text-sm text-orange-500 active:scale-95"
        >
          ＋ 加一道菜
        </button>

        <div className="mt-4 flex gap-2">
          <button onClick={onClose} className="flex-1 rounded-xl bg-stone-100 py-2.5 text-sm active:scale-95">
            取消
          </button>
          <button
            onClick={() => { onSave(time.trim(), cleaned); onClose(); }}
            disabled={cleaned.length === 0}
            className="flex-1 rounded-xl bg-orange-500 py-2.5 text-sm font-medium text-white active:scale-95 disabled:bg-stone-300"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
