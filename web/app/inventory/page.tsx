"use client";

// 库存页：列表 / 新增(累加) / 改份数(精确) / 删除 + 「扫订单入库」。
// 行为对齐 iOS InventoryView：
//   - 扫订单是 解析→预览可改→确认 三步，视觉解析不可靠，绝不静默入库；
//   - 确认入库逐条调 add，【累计失败项】最后一起报——不让后面的成功把前面的失败冲没。
import { useCallback, useEffect, useRef, useState } from "react";
import { api, freshnessBadge, type InventoryItem } from "@/lib/api";
import { compressForUpload } from "@/lib/image";

// 份数渲染：整数不带小数点（2），小数保留（0.5）。和后端 fmtQty 一个口径。
const fmtQty = (q: number) => (Number.isInteger(q) ? String(q) : String(q));

interface ParsedItem {
  name: string;
  quantity: number;
  unit: string;
}

export default function InventoryPage() {
  const [items, setItems] = useState<InventoryItem[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<InventoryItem | null>(null);
  const [adding, setAdding] = useState(false);
  const [parsing, setParsing] = useState(false);
  const [parsed, setParsed] = useState<ParsedItem[] | null>(null); // 非 null = 预览确认中
  const [quickAdding, setQuickAdding] = useState(false); // 一句话入库弹层
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try {
      setItems(await api.inventory());
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function write(op: "add" | "set" | "remove", name: string, quantity: number, unit: string) {
    try {
      setItems(await api.inventoryWrite(op, name, quantity, unit));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function onPickImage(file: File | null) {
    if (!file) return;
    setParsing(true);
    setError("");
    try {
      const { base64, mime } = await compressForUpload(file);
      const list = await api.parseOrderImage(base64, mime);
      if (list.length === 0) {
        setError("没识别到食材，换张清晰点的订单截图试试");
      } else {
        setParsed(list);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setParsing(false);
      if (fileRef.current) fileRef.current.value = ""; // 重置，允许再选同一张
    }
  }

  // 一句话入库：家长打一句「买了两块鳕鱼、一个西兰花」，解析成条目（不入库），
  // 汇到和扫订单同一个确认页——不管来源是图还是话，进账前都要过一遍家长的眼睛。
  //
  // 这条路存在的理由：入库原本只有截图那一条，要切到买菜 App、截图、回来上传、
  // 等视觉模型，末了相册里还多一张再也不会看的图。摩擦全堆在入库这一端，
  // 家长自然就不记了——账本失准的根不在算法，在这儿。
  async function onParseText(text: string) {
    setParsing(true);
    setError("");
    try {
      const list = await api.parseInventoryText(text);
      if (list.length === 0) {
        setError("没从这句话里识别到食材，换个说法试试");
        return false;
      }
      setParsed(list);
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      return false;
    } finally {
      setParsing(false);
    }
  }

  // 确认入库：走后端 add_batch【一次性】写入。
  // 原来是逐条 add + 累计失败项，问题是中途失败会留下「入了一半」的账，
  // 而家长在确认页看到的是完整一批，回头对不上。
  async function confirmParsed(list: ParsedItem[]) {
    setParsed(null);
    try {
      setItems(await api.inventoryAddBatch(list));
      setError("");
    } catch (e) {
      setError(`入库失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-stone-200 bg-white px-4 py-3">
        <span className="font-semibold">家庭库存</span>
        <div className="flex gap-3 text-sm">
          {/* 一句话入库排在最前是有意的：它是入库的主路径，截图那条摩擦大得多。 */}
          <button onClick={() => setQuickAdding(true)} className="text-orange-500 active:scale-95">
            💬 一句话
          </button>
          <button onClick={() => fileRef.current?.click()} className="text-orange-500 active:scale-95">
            🧾 扫订单
          </button>
          <button onClick={() => setAdding(true)} className="text-orange-500 active:scale-95">
            ＋ 添加
          </button>
        </div>
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={(e) => onPickImage(e.target.files?.[0] ?? null)}
        />
      </header>

      {error && (
        <div className="flex items-center justify-between bg-red-50 px-4 py-2 text-sm text-red-600">
          <span className="min-w-0 flex-1">{error}</span>
          <button onClick={() => setError("")} className="ml-2 shrink-0">✕</button>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {!loaded ? (
          <p className="pt-16 text-center text-sm text-stone-400">加载中…</p>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center gap-2 pt-16 text-center">
            <div className="text-3xl">📦</div>
            <p className="text-sm text-stone-500">库存是空的<br />右上角 ＋ 添加，或在聊天里让助手记账</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-2xl bg-white shadow-sm">
            {items.map((it, i) => (
              <div
                key={it.name}
                className={`flex items-center gap-2 px-4 py-3 ${i > 0 ? "border-t border-stone-100" : ""}`}
              >
                <button onClick={() => setEditing(it)} className="min-w-0 flex-1 text-left active:opacity-60">
                  <span className="text-[15px]">{it.name}</span>
                  <span className="ml-2 tabular-nums text-sm text-stone-500">
                    {fmtQty(it.quantity)} {it.unit}
                  </span>
                  {/* 新鲜度徽章：只标「该吃了」和「可能没了」——全都标一遍等于没标。
                      后端已按紧急度排序，所以要处理的东西天然聚在列表顶部。 */}
                  {(() => {
                    const badge = freshnessBadge(it);
                    return badge ? (
                      <span className={`ml-2 text-xs ${badge.urgent ? "text-red-500" : "text-orange-500"}`}>
                        {badge.text}
                      </span>
                    ) : null;
                  })()}
                </button>
                <button
                  onClick={() => write("remove", it.name, 0, "")}
                  className="shrink-0 text-xs text-stone-300 active:text-red-500"
                  aria-label={`删除${it.name}`}
                >
                  删除
                </button>
              </div>
            ))}
            <div className="border-t border-stone-100 px-4 py-2 text-xs text-stone-400">
              点条目改份数；改账和聊天里助手记的是同一本。
            </div>
          </div>
        )}
      </div>

      {parsing && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/20">
          <div className="rounded-2xl bg-white px-6 py-5 text-sm text-stone-600 shadow-lg">
            {/* 订单截图和一句话共用这个遮罩，文案别写死成「订单」。 */}
            正在识别…
          </div>
        </div>
      )}

      {(editing || adding) && (
        <ItemEditorSheet
          item={editing}
          onSave={(name, qty, unit) => {
            // 编辑现有 = set（精确值）；新增 = add（累加语义，同名会加上去）。
            if (editing) write("set", editing.name, qty, unit);
            else write("add", name, qty, unit);
          }}
          onClose={() => { setEditing(null); setAdding(false); }}
        />
      )}

      {quickAdding && (
        <QuickAddSheet
          busy={parsing}
          onParse={async (text) => {
            const ok = await onParseText(text);
            if (ok) setQuickAdding(false); // 解析成功就让位给确认页
            return ok;
          }}
          onClose={() => setQuickAdding(false)}
        />
      )}

      {parsed && (
        <ParsedOrderSheet
          initial={parsed}
          onConfirm={confirmParsed}
          onClose={() => setParsed(null)}
        />
      )}
    </div>
  );
}

// 一句话入库弹层：打一句买了什么，交给后端解析成条目，再走和扫订单同一个确认页。
// 网页端没有 iOS 那个原生语音输入，但手机键盘自带的听写按钮同样能往输入框里说话。
function QuickAddSheet({
  busy,
  onParse,
  onClose,
}: {
  busy: boolean;
  onParse: (text: string) => Promise<boolean>;
  onClose: () => void;
}) {
  const [text, setText] = useState("");
  const trimmed = text.trim();

  return (
    <div className="fixed inset-0 z-20 flex items-end bg-black/30" onClick={onClose}>
      <div
        className="w-full rounded-t-3xl bg-white p-4 pb-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-1 font-semibold">一句话入库</div>
        <div className="mb-3 text-xs text-stone-500">
          说说买了什么，比如「买了两块鳕鱼、一个西兰花、一盒鸡蛋」
        </div>
        <textarea
          autoFocus
          rows={3}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="买了…"
          disabled={busy}
          className="w-full resize-none rounded-xl border border-stone-300 px-3 py-2 text-[15px] outline-none focus:border-orange-400"
        />
        <div className="mt-3 flex gap-2">
          <button
            onClick={onClose}
            className="flex-1 rounded-xl border border-stone-300 py-2.5 text-sm text-stone-600 active:scale-95"
          >
            取消
          </button>
          <button
            disabled={busy || trimmed === ""}
            onClick={() => onParse(trimmed)}
            className="flex-1 rounded-xl bg-orange-500 py-2.5 text-sm text-white active:scale-95 disabled:opacity-50"
          >
            {busy ? "识别中…" : "识别"}
          </button>
        </div>
      </div>
    </div>
  );
}

// 单条编辑/新增弹层。编辑时名字锁死（name 是主键，改名请删了重加）。
function ItemEditorSheet({
  item,
  onSave,
  onClose,
}: {
  item: InventoryItem | null;
  onSave: (name: string, qty: number, unit: string) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState(item?.name ?? "");
  const [qty, setQty] = useState(item ? fmtQty(item.quantity) : "1");
  const [unit, setUnit] = useState(item?.unit ?? "份");
  const q = parseFloat(qty);
  const valid = name.trim() !== "" && !isNaN(q) && q > 0;

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/30" onClick={onClose}>
      <div
        className="w-full max-w-lg rounded-t-2xl bg-white p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 text-center font-semibold">{item ? `改「${item.name}」` : "添加食材"}</div>
        {!item && (
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="食材名，如 鳕鱼"
            autoFocus
            className="mb-2 w-full rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
          />
        )}
        <div className="flex gap-2">
          <input
            value={qty}
            onChange={(e) => setQty(e.target.value)}
            inputMode="decimal"
            placeholder="份数"
            className="w-2/3 rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
          />
          <input
            value={unit}
            onChange={(e) => setUnit(e.target.value)}
            placeholder="单位"
            className="w-1/3 rounded-xl bg-stone-100 px-3.5 py-2.5 text-sm outline-none focus:ring-1 focus:ring-orange-300"
          />
        </div>
        <div className="mt-3 flex gap-2">
          <button onClick={onClose} className="flex-1 rounded-xl bg-stone-100 py-2.5 text-sm active:scale-95">
            取消
          </button>
          <button
            onClick={() => { onSave(name.trim(), q, unit.trim()); onClose(); }}
            disabled={!valid}
            className="flex-1 rounded-xl bg-orange-500 py-2.5 text-sm font-medium text-white active:scale-95 disabled:bg-stone-300"
          >
            保存
          </button>
        </div>
      </div>
    </div>
  );
}

// 订单识别结果预览：每条可改名字/份数/单位、可删，确认后全部入库。
function ParsedOrderSheet({
  initial,
  onConfirm,
  onClose,
}: {
  initial: ParsedItem[];
  onConfirm: (items: ParsedItem[]) => void;
  onClose: () => void;
}) {
  const [rows, setRows] = useState(initial.map((r) => ({ ...r, qtyStr: fmtQty(r.quantity) })));
  const cleaned = rows
    .map((r) => ({ name: r.name.trim(), quantity: parseFloat(r.qtyStr), unit: r.unit.trim() }))
    .filter((r) => r.name !== "" && !isNaN(r.quantity) && r.quantity > 0);

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/30" onClick={onClose}>
      <div
        className="max-h-[85dvh] w-full max-w-lg overflow-y-auto rounded-t-2xl bg-white p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-1 text-center font-semibold">识别出 {rows.length} 项</div>
        <p className="mb-3 text-center text-xs text-stone-400">核对/修改后确认入库；同名食材会累加</p>
        <div className="space-y-2">
          {rows.map((r, i) => (
            <div key={i} className="flex items-center gap-2 rounded-xl bg-stone-50 p-2.5">
              <input
                value={r.name}
                onChange={(e) => setRows((p) => p.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                className="min-w-0 flex-1 rounded-lg bg-white px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-orange-300"
              />
              <input
                value={r.qtyStr}
                onChange={(e) => setRows((p) => p.map((x, j) => (j === i ? { ...x, qtyStr: e.target.value } : x)))}
                inputMode="decimal"
                className="w-14 rounded-lg bg-white px-2 py-2 text-center text-sm outline-none focus:ring-1 focus:ring-orange-300"
              />
              <input
                value={r.unit}
                onChange={(e) => setRows((p) => p.map((x, j) => (j === i ? { ...x, unit: e.target.value } : x)))}
                className="w-14 rounded-lg bg-white px-2 py-2 text-center text-sm outline-none focus:ring-1 focus:ring-orange-300"
              />
              <button
                onClick={() => setRows((p) => p.filter((_, j) => j !== i))}
                className="shrink-0 text-stone-400"
                aria-label="删除这项"
              >
                ✕
              </button>
            </div>
          ))}
        </div>
        <div className="mt-3 flex gap-2">
          <button onClick={onClose} className="flex-1 rounded-xl bg-stone-100 py-2.5 text-sm active:scale-95">
            取消
          </button>
          <button
            onClick={() => onConfirm(cleaned)}
            disabled={cleaned.length === 0}
            className="flex-1 rounded-xl bg-orange-500 py-2.5 text-sm font-medium text-white active:scale-95 disabled:bg-stone-300"
          >
            确认入库 {cleaned.length} 项
          </button>
        </div>
      </div>
    </div>
  );
}
