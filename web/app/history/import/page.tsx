"use client";

// 导入历史页：粘贴文字 或 选菜单/记录截图 → 模型解析成结构化历史 → 预览核对 → 确认写入。
// 解析→预览→确认三步（对齐 iOS ImportHistoryView）：防坏解析静默污染历史（同日同餐是覆盖）。
import { useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { api, type Day } from "@/lib/api";
import { compressForUpload } from "@/lib/image";
import MealRow from "@/components/MealRow";

export default function ImportHistoryPage() {
  const router = useRouter();
  const [mode, setMode] = useState<"text" | "image">("text");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(""); // 非空 = 遮罩文案
  const [preview, setPreview] = useState<Day[]>([]);
  const [error, setError] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  async function parseText() {
    setBusy("正在解析…");
    setError("");
    try {
      setPreview(await api.parseHistoryText(text));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  }

  async function parseImage(file: File | null) {
    if (!file) return;
    setBusy("正在解析…");
    setError("");
    try {
      const { base64, mime } = await compressForUpload(file);
      setPreview(await api.parseHistoryImage(base64, mime));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  async function confirmImport() {
    setBusy("正在写入历史…");
    setError("");
    try {
      const res = await api.importHistory(preview);
      alert(`导入成功：新增 ${res.added} 餐${res.replaced > 0 ? `，覆盖 ${res.replaced} 餐` : ""}`);
      router.push("/history");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-stone-200 bg-white px-4 py-3">
        <button onClick={() => router.push("/history")} className="text-sm text-stone-500 active:scale-95">
          ‹ 返回
        </button>
        <span className="font-semibold">导入历史</span>
        <span className="w-10" />
      </header>

      {error && (
        <div className="flex items-center justify-between bg-red-50 px-4 py-2 text-sm text-red-600">
          <span className="min-w-0 flex-1">{error}</span>
          <button onClick={() => setError("")} className="ml-2 shrink-0">✕</button>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {preview.length === 0 ? (
          <div className="space-y-3">
            <div className="flex rounded-xl bg-stone-100 p-1">
              {(["text", "image"] as const).map((m) => (
                <button
                  key={m}
                  onClick={() => setMode(m)}
                  className={`flex-1 rounded-lg py-1.5 text-sm transition ${
                    mode === m ? "bg-white font-medium shadow-sm" : "text-stone-500"
                  }`}
                >
                  {m === "text" ? "粘贴文字" : "选图片"}
                </button>
              ))}
            </div>

            {mode === "text" ? (
              <>
                <textarea
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  placeholder={"粘贴历史（微信记录 / 备忘录都行）\n带上日期和三餐，格式随意——模型会自动整理。"}
                  rows={9}
                  className="w-full resize-none rounded-2xl bg-white p-3.5 text-sm shadow-sm outline-none focus:ring-1 focus:ring-orange-300"
                />
                <button
                  onClick={parseText}
                  disabled={!text.trim()}
                  className="w-full rounded-xl bg-orange-500 py-2.5 text-sm font-medium text-white active:scale-95 disabled:bg-stone-300"
                >
                  ✨ 解析
                </button>
              </>
            ) : (
              <button
                onClick={() => fileRef.current?.click()}
                className="flex w-full flex-col items-center gap-2 rounded-2xl border-2 border-dashed border-stone-300 bg-white py-10 text-sm text-stone-500 active:scale-[0.99]"
              >
                <span className="text-3xl">🖼️</span>
                选一张菜单 / 记录截图（选中后自动解析）
              </button>
            )}
            <input
              ref={fileRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={(e) => parseImage(e.target.files?.[0] ?? null)}
            />
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-center text-xs text-stone-400">
              解析出 {preview.length} 天 · 点 ✕ 删掉不对的 · 同一天同一餐会覆盖已有记录
            </p>
            {preview.map((day, i) => (
              <section key={day.date} className="rounded-2xl bg-white p-3.5 shadow-sm">
                <div className="mb-2 flex items-center justify-between">
                  <span className="text-sm font-bold">{day.date}</span>
                  <button
                    onClick={() => setPreview((p) => p.filter((_, j) => j !== i))}
                    className="text-stone-300 active:text-red-500"
                    aria-label={`删除${day.date}`}
                  >
                    ✕
                  </button>
                </div>
                <div className="space-y-2.5">
                  <MealRow label="午餐" meal={day.lunch} />
                  <MealRow label="水果" meal={day.fruit} />
                  <MealRow label="晚餐" meal={day.dinner} />
                </div>
              </section>
            ))}
            <div className="flex gap-2">
              <button
                onClick={() => setPreview([])}
                className="flex-1 rounded-xl bg-stone-100 py-2.5 text-sm active:scale-95"
              >
                重来
              </button>
              <button
                onClick={confirmImport}
                disabled={preview.length === 0}
                className="flex-1 rounded-xl bg-orange-500 py-2.5 text-sm font-medium text-white active:scale-95 disabled:bg-stone-300"
              >
                确认导入 {preview.length} 天
              </button>
            </div>
          </div>
        )}
      </div>

      {busy && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/20">
          <div className="rounded-2xl bg-white px-6 py-5 text-sm text-stone-600 shadow-lg">{busy}</div>
        </div>
      )}
    </div>
  );
}
