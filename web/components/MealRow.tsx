"use client";

// MealRow —— 一餐的渲染：图标 + 标签 + 时间徽章 + 菜品清单。
// 反馈是【菜级粒度】（对齐 iOS MealRowView）：每道菜一行，行尾是这道菜自己的
// 反馈徽章 / 「反馈」按钮；旧数据的餐级反馈显示成只读「整餐」小徽章。
// onFeedback 不传 = 只读展示（导入预览等场景复用）。
import type { Feedback, Meal } from "@/lib/api";

const STYLE: Record<string, { icon: string; tint: string }> = {
  午餐: { icon: "☀️", tint: "bg-orange-100" },
  水果: { icon: "🍃", tint: "bg-green-100" },
  晚餐: { icon: "🌙", tint: "bg-indigo-100" },
};

export function feedbackLabel(fb: Feedback): string {
  const emoji = { like: "👍", dislike: "👎", ok: "😐" }[fb.rating] ?? "•";
  const label = { like: "爱吃", dislike: "不爱吃", ok: "一般" }[fb.rating] ?? fb.rating;
  return fb.note ? `${emoji} ${label} · ${fb.note}` : `${emoji} ${label}`;
}

export function feedbackTint(fb: Feedback): string {
  return (
    { like: "text-green-600 bg-green-50", dislike: "text-red-500 bg-red-50", ok: "text-amber-600 bg-amber-50" }[
      fb.rating
    ] ?? "text-stone-500 bg-stone-100"
  );
}

export default function MealRow({
  label,
  meal,
  onFeedback,
}: {
  label: string;
  meal?: Meal | null;
  // 点了某道菜的反馈入口：(菜名, 现有反馈)。不传 = 只读。
  onFeedback?: (dish: string, current: Feedback | null) => void;
}) {
  if (!meal || meal.dishes.length === 0) return null;
  const s = STYLE[label] ?? { icon: "🍽", tint: "bg-stone-100" };

  return (
    <div className="flex gap-2.5">
      <div className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-sm ${s.tint}`}>
        {s.icon}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="text-sm font-semibold">{label}</span>
          {meal.time && (
            <span className="rounded-full bg-stone-100 px-1.5 py-0.5 text-[11px] tabular-nums text-stone-500">
              {meal.time}
            </span>
          )}
          {meal.feedback && (
            <span className={`rounded-full px-1.5 py-0.5 text-[11px] ${feedbackTint(meal.feedback)}`}>
              整餐 {feedbackLabel(meal.feedback)}
            </span>
          )}
        </div>
        <div className="mt-1 space-y-1.5">
          {meal.dishes.map((d) => (
            <div key={d.name} className="flex items-start gap-2">
              <div className="min-w-0 flex-1">
                <div className="text-[15px]">{d.name}</div>
                {d.detail && <div className="text-xs text-stone-500">{d.detail}</div>}
              </div>
              {onFeedback ? (
                <button
                  onClick={() => onFeedback(d.name, d.feedback ?? null)}
                  className={`shrink-0 rounded-full px-2 py-0.5 text-xs transition active:scale-95 ${
                    d.feedback ? feedbackTint(d.feedback) : "text-orange-500"
                  }`}
                >
                  {d.feedback ? feedbackLabel(d.feedback) : "＋反馈"}
                </button>
              ) : (
                d.feedback && (
                  <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs ${feedbackTint(d.feedback)}`}>
                    {feedbackLabel(d.feedback)}
                  </span>
                )
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
