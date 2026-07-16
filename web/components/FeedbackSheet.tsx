"use client";

// FeedbackSheet —— 给某道菜记食用反馈的底部弹层（对齐 iOS FeedbackEditorSheet）：
// 选 爱吃/一般/不爱吃 + 可选备注；已有反馈时多一个「清除」。
// rating 空串 = 清除，与后端 /api/history/feedback 的语义一致。
import { useState } from "react";
import type { Feedback } from "@/lib/api";

const OPTIONS = [
  { value: "like", label: "👍 爱吃" },
  { value: "ok", label: "😐 一般" },
  { value: "dislike", label: "👎 不爱吃" },
];

export default function FeedbackSheet({
  dishName,
  current,
  onSave,
  onClose,
}: {
  dishName: string;
  current: Feedback | null;
  onSave: (rating: string, note: string) => void;
  onClose: () => void;
}) {
  const [rating, setRating] = useState(current?.rating ?? "like");
  const [note, setNote] = useState(current?.note ?? "");

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/30" onClick={onClose}>
      <div
        className="w-full max-w-lg rounded-t-2xl bg-white p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 text-center font-semibold">宝宝爱吃「{dishName}」吗</div>

        <div className="flex gap-2">
          {OPTIONS.map((o) => (
            <button
              key={o.value}
              onClick={() => setRating(o.value)}
              className={`flex-1 rounded-xl py-2.5 text-sm transition active:scale-95 ${
                rating === o.value ? "bg-orange-500 font-medium text-white" : "bg-stone-100"
              }`}
            >
              {o.label}
            </button>
          ))}
        </div>

        <input
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="备注（可选）：如 只吃了几口 / 换个做法就行"
          className="mt-3 w-full rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
        />

        <div className="mt-3 flex gap-2">
          {current && (
            <button
              onClick={() => { onSave("", ""); onClose(); }}
              className="flex-1 rounded-xl bg-red-50 py-2.5 text-sm text-red-500 transition active:scale-95"
            >
              清除反馈
            </button>
          )}
          <button
            onClick={onClose}
            className="flex-1 rounded-xl bg-stone-100 py-2.5 text-sm transition active:scale-95"
          >
            取消
          </button>
          <button
            onClick={() => { onSave(rating, note.trim()); onClose(); }}
            className="flex-1 rounded-xl bg-orange-500 py-2.5 text-sm font-medium text-white transition active:scale-95"
          >
            保存
          </button>
        </div>
      </div>
    </div>
  );
}
