"use client";

// 档案页：宝宝称呼/生日(算月龄)/过敏原/忌口/要点 就地编辑 + 只读的「偏好规律」。
// 语义对齐 iOS ProfileView / 后端 ProfileStore.Update：
//   - 空字符串字段 = 保持原值；数组始终整组提交（界面清空 = 真清空）；
//   - 生日只在「已记录」时提交（不动后端已有值）；
//   - 偏好规律是后端按菜级反馈归纳的派生数据，只展示不参与保存。
import { useCallback, useEffect, useState } from "react";
import { api, type PrefRule } from "@/lib/api";

function ageText(birth: string): string {
  const d = new Date(`${birth}T00:00:00`);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  let months = (now.getFullYear() - d.getFullYear()) * 12 + (now.getMonth() - d.getMonth());
  if (now.getDate() < d.getDate()) months--;
  months = Math.max(months, 0);
  if (months === 0) return "还不满 1 个月";
  if (months < 12) return `现在 ${months} 个月大`;
  const y = Math.floor(months / 12);
  const m = months % 12;
  return `现在 ${y} 岁${m > 0 ? ` ${m} 个月` : ""}（共 ${months} 个月）`;
}

export default function ProfilePage() {
  const [babyName, setBabyName] = useState("");
  const [birthDate, setBirthDate] = useState(""); // 空串 = 未记录
  const [allergies, setAllergies] = useState<string[]>([]);
  const [dislikes, setDislikes] = useState<string[]>([]);
  const [notes, setNotes] = useState("");
  const [rules, setRules] = useState<PrefRule[]>([]);

  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [savedNote, setSavedNote] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      const p = await api.profile();
      setBabyName(p.babyName ?? "");
      setBirthDate(p.birthDate ?? "");
      setAllergies(p.allergies ?? []);
      setDislikes(p.dislikes ?? []);
      setNotes(p.notes ?? "");
      setRules(p.rules ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function save() {
    setSaving(true);
    setError("");
    setSavedNote("");
    try {
      const p = await api.updateProfile({
        babyName: babyName.trim(),
        birthDate: birthDate || undefined,
        allergies,
        dislikes,
        notes: notes.trim(),
      });
      setRules(p.rules ?? []);
      setSavedNote("已保存 ✓");
      setTimeout(() => setSavedNote(""), 2000);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  if (!loaded) return <p className="pt-16 text-center text-sm text-stone-400">加载中…</p>;

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-stone-200 bg-white px-4 py-3">
        <span className="font-semibold">宝宝档案</span>
        <button
          onClick={save}
          disabled={saving}
          className="text-sm text-orange-500 active:scale-95 disabled:text-stone-300"
        >
          {saving ? "保存中…" : "保存"}
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        <div className="space-y-3">
          {savedNote && <div className="rounded-xl bg-green-50 px-3.5 py-2 text-sm text-green-600">{savedNote}</div>}
          {error && <div className="rounded-xl bg-red-50 px-3.5 py-2 text-sm text-red-600">{error}</div>}

          <section className="rounded-2xl bg-white p-3.5 shadow-sm">
            <div className="mb-2 text-sm font-semibold">宝宝</div>
            <input
              value={babyName}
              onChange={(e) => setBabyName(e.target.value)}
              placeholder="称呼（如 宝宝 / 朵朵）"
              className="w-full rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
            />
            <div className="mt-2 flex items-center gap-2">
              <input
                type="date"
                value={birthDate}
                onChange={(e) => setBirthDate(e.target.value)}
                max={new Date().toISOString().slice(0, 10)}
                className="flex-1 rounded-xl bg-stone-100 px-3.5 py-2 text-sm outline-none focus:ring-1 focus:ring-orange-300"
              />
              {birthDate && (
                <button onClick={() => setBirthDate("")} className="shrink-0 text-xs text-stone-400">
                  清除
                </button>
              )}
            </div>
            {birthDate && <div className="mt-1.5 text-xs text-stone-500">{ageText(birthDate)}</div>}
          </section>

          <TagEditor
            title="过敏源"
            placeholder="添加过敏原，如 鸡蛋"
            footer="硬禁忌：推荐时会【绝对排除】这些食材及其制品。"
            items={allergies}
            onChange={setAllergies}
            chip="bg-red-50 text-red-600"
          />
          <TagEditor
            title="忌口 / 不爱吃"
            placeholder="添加忌口，如 香菜"
            footer="软偏好：推荐时尽量避开或换个做法。"
            items={dislikes}
            onChange={setDislikes}
            chip="bg-amber-50 text-amber-700"
          />

          <section className="rounded-2xl bg-white p-3.5 shadow-sm">
            <div className="mb-2 text-sm font-semibold">其他要点</div>
            <textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="咀嚼能力、口味倾向等（可留空）"
              rows={2}
              className="w-full resize-none rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
            />
          </section>

          {rules.length > 0 && (
            <section className="rounded-2xl bg-white p-3.5 shadow-sm">
              <div className="mb-2 text-sm font-semibold">偏好规律（自动归纳）</div>
              <div className="space-y-2.5">
                {rules.map((r) => (
                  <div key={r.name}>
                    <div className="flex items-center gap-1.5">
                      <span className="text-[15px] font-medium">{r.name}</span>
                      {r.likes > 0 && <Badge text={`👍${r.likes}`} cls="bg-green-50 text-green-600" />}
                      {r.dislikes > 0 && <Badge text={`👎${r.dislikes}`} cls="bg-red-50 text-red-500" />}
                      {r.oks > 0 && <Badge text={`😐${r.oks}`} cls="bg-amber-50 text-amber-600" />}
                    </div>
                    <div className="text-xs text-stone-500">{r.advice}</div>
                  </div>
                ))}
              </div>
              <p className="mt-2.5 text-xs text-stone-400">
                依据每道菜的食用反馈自动归纳，随反馈更新。推荐时：爱吃的适当多排，不爱吃的少排但不会完全不推；过敏原才是绝对排除。
              </p>
            </section>
          )}
        </div>
      </div>
    </div>
  );
}

function Badge({ text, cls }: { text: string; cls: string }) {
  return <span className={`rounded-full px-1.5 py-0.5 text-[11px] ${cls}`}>{text}</span>;
}

// 一组可增删的标签（过敏源、忌口共用）：胶囊 ✕ 删除，底部输入回车/点＋新增，去重。
function TagEditor({
  title,
  placeholder,
  footer,
  items,
  onChange,
  chip,
}: {
  title: string;
  placeholder: string;
  footer: string;
  items: string[];
  onChange: (items: string[]) => void;
  chip: string;
}) {
  const [input, setInput] = useState("");

  function add() {
    const t = input.trim();
    setInput("");
    if (!t || items.includes(t)) return;
    onChange([...items, t]);
  }

  return (
    <section className="rounded-2xl bg-white p-3.5 shadow-sm">
      <div className="mb-2 text-sm font-semibold">{title}</div>
      {items.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {items.map((it) => (
            <span key={it} className={`flex items-center gap-1 rounded-full px-3 py-1 text-sm ${chip}`}>
              {it}
              <button onClick={() => onChange(items.filter((x) => x !== it))} aria-label={`删除${it}`}>✕</button>
            </span>
          ))}
        </div>
      )}
      <div className="flex gap-2">
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && add()}
          placeholder={placeholder}
          className="min-w-0 flex-1 rounded-xl bg-stone-100 px-3.5 py-2 text-sm outline-none focus:ring-1 focus:ring-orange-300"
        />
        <button
          onClick={add}
          disabled={!input.trim()}
          className="shrink-0 rounded-xl bg-orange-500 px-3.5 text-sm text-white active:scale-95 disabled:bg-stone-300"
        >
          ＋
        </button>
      </div>
      <p className="mt-2 text-xs text-stone-400">{footer}</p>
    </section>
  );
}
