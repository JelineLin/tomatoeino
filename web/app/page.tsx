"use client";

// 聊天页（首页）：消息气泡 + 底部输入框，助手回复流式打字机。
// 行为对齐 iOS ChatView：
//   - thinking 事件进灰色可折叠区（流式期间自动展开，答案一出现自动收起）；
//   - context（工具备忘）存在那条助手消息上，下一轮随历史带回（L1 回灌）；
//   - session 钥匙 + 消息一起落 localStorage：切 tab / 刷新 / 关浏览器都能接上。
//     切 tab 必须落盘——底部导航是 <Link>，路由一变本页组件就被卸载重建，
//     存在组件里的东西全没（iOS 的 TabView 会留住页面，所以只有网页版会犯）。
//     钥匙过期也不怕：后端命不中就用一并存下的全量消息走 L1 重建。
import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import { streamChat, loadChatState, saveChatState, type ChatMessage } from "@/lib/api";

interface Bubble extends ChatMessage {
  id: number;
  thinking: string;
  thinkingOpen: boolean;
}

const SUGGESTIONS = [
  "最近三天吃了啥？",
  "明天三餐帮我配一下，别和最近重样",
  "这个月应季的水果有哪些？",
];

export default function ChatPage() {
  const [messages, setMessages] = useState<Bubble[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const sessionID = useRef("");
  const nextID = useRef(1);
  const bottomRef = useRef<HTMLDivElement>(null);
  // 水合完成前不回存，否则首帧那个空数组会把存好的对话覆盖掉。
  // 必须用 state 而不是 ref：同一个 commit 里 effect 按声明顺序跑，
  // 水合那个先把 ref 置 true，回存那个紧接着就以为可以存了——可它闭包里的
  // messages 还是挂载时的空数组，一存就把刚捞回来的对话抹了。
  // 换成 state 后，回存 effect 首次运行读到的是初始值 false，老实跳过；
  // 等 setRestored 触发重渲染，那一轮的 messages 才是恢复后的。
  const [restored, setRestored] = useState(false);

  // 进页面先从 localStorage 捞回上次的对话。放 useEffect 而不是 useState 初始值：
  // 静态导出会预渲染这个组件，构建时没有 window，直接读会炸。
  useEffect(() => {
    const saved = loadChatState<Bubble>();
    if (saved) {
      sessionID.current = saved.sessionID;
      if (saved.messages.length > 0) {
        setMessages(saved.messages);
        // 气泡 id 是自增的，接着最大值往后发，免得和捞回来的撞号。
        nextID.current = Math.max(...saved.messages.map((m) => m.id)) + 1;
      }
    }
    setRestored(true);
  }, []);

  // 消息一变就回存。sessionID 是 ref 不触发渲染，跟着这里搭车存下去即可。
  useEffect(() => {
    if (!restored) return;
    saveChatState(sessionID.current, messages);
  }, [messages, restored]);

  // 任何内容增量都跟着滚到底（打字机的「跟手感」）。
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages]);

  async function send(text: string) {
    const t = text.trim();
    if (!t || sending) return;
    setSending(true);
    setInput("");

    const userMsg: Bubble = { id: nextID.current++, role: "user", content: t, thinking: "", thinkingOpen: false };
    const asstID = nextID.current++;
    // 发给后端的历史 = 到用户这条为止（不含助手占位）。
    const history: ChatMessage[] = [...messages, userMsg].map((m) => ({
      role: m.role,
      content: m.content,
      context: m.context,
    }));
    setMessages((prev) => [
      ...prev,
      userMsg,
      { id: asstID, role: "assistant", content: "", thinking: "", thinkingOpen: true },
    ]);

    const patch = (f: (m: Bubble) => Bubble) =>
      setMessages((prev) => prev.map((m) => (m.id === asstID ? f(m) : m)));

    try {
      for await (const ev of streamChat(history, sessionID.current)) {
        switch (ev.type) {
          case "thinking":
            patch((m) => ({ ...m, thinking: m.thinking + ev.text }));
            break;
          case "answer":
            // 答案一开始出现就把思考区收起——注意力该转向结论了（和 iOS 同款交互）。
            patch((m) => ({ ...m, content: m.content + ev.text, thinkingOpen: false }));
            break;
          case "context":
            patch((m) => ({ ...m, context: ev.text }));
            break;
          case "session":
            sessionID.current = ev.text;
            break;
        }
      }
    } catch (e) {
      patch((m) => ({ ...m, content: m.content + `\n⚠️ ${e instanceof Error ? e.message : e}` }));
    } finally {
      setSending(false);
      // 显式再存一次：会话钥匙是流【末尾】才到的，而它存在 ref 里不触发上面那个
      // 跟着 messages 走的回存。漏了这一下，切个 tab 回来钥匙就丢了——
      // 虽然还能靠 L1 全量重发续上，但每轮都要重灌历史，白烧 token。
      setMessages((prev) => {
        saveChatState(sessionID.current, prev);
        return prev;
      });
    }
  }

  return (
    <div className="flex h-full flex-col">
      <header className="shrink-0 border-b border-stone-200 bg-white py-3 text-center font-semibold">
        备餐助手
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-4">
        {messages.length === 0 && <EmptyState onPick={send} />}
        <div className="space-y-3">
          {messages.map((m) => (
            <MessageBubble
              key={m.id}
              msg={m}
              onToggleThinking={(open) =>
                setMessages((prev) =>
                  prev.map((x) => (x.id === m.id ? { ...x, thinkingOpen: open } : x)),
                )
              }
            />
          ))}
        </div>
        <div ref={bottomRef} />
      </div>

      <div className="shrink-0 border-t border-stone-200 bg-white p-2">
        <div className="flex items-end gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                send(input);
              }
            }}
            placeholder="说点什么…"
            rows={1}
            className="max-h-28 min-h-[42px] flex-1 resize-none rounded-2xl bg-stone-100 px-4 py-2.5 outline-none focus:bg-stone-50 focus:ring-1 focus:ring-orange-300"
          />
          <button
            onClick={() => send(input)}
            disabled={sending || !input.trim()}
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-orange-500 text-white transition active:scale-90 disabled:bg-stone-300"
            aria-label="发送"
          >
            {sending ? <Spinner /> : "↑"}
          </button>
        </div>
      </div>
    </div>
  );
}

function EmptyState({ onPick }: { onPick: (t: string) => void }) {
  return (
    <div className="flex flex-col items-center gap-4 pt-14 text-center">
      <div className="text-5xl">🍽️</div>
      <div className="text-lg font-bold">今天吃点啥？</div>
      <p className="text-sm text-stone-500">我会先翻宝宝的吃饭历史和时令表，再给建议</p>
      <div className="mt-2 flex flex-col gap-2">
        {SUGGESTIONS.map((s) => (
          <button
            key={s}
            onClick={() => onPick(s)}
            className="rounded-full bg-orange-50 px-5 py-2.5 text-sm text-orange-600 transition active:scale-95"
          >
            {s}
          </button>
        ))}
      </div>
    </div>
  );
}

function MessageBubble({
  msg,
  onToggleThinking,
}: {
  msg: Bubble;
  onToggleThinking: (open: boolean) => void;
}) {
  if (msg.role === "user") {
    return (
      <div className="flex justify-end pl-12">
        <div className="whitespace-pre-wrap rounded-2xl rounded-br-md bg-gradient-to-br from-orange-500 to-orange-600 px-4 py-2.5 text-white">
          {msg.content}
        </div>
      </div>
    );
  }
  const streaming = !msg.content;
  return (
    <div className="flex pr-12">
      <div className="min-w-0 rounded-2xl rounded-bl-md bg-white px-4 py-2.5 shadow-sm">
        {msg.thinking && (
          <details
            open={msg.thinkingOpen}
            onToggle={(e) => onToggleThinking((e.target as HTMLDetailsElement).open)}
            className="mb-1"
          >
            <summary className="cursor-pointer select-none text-xs text-stone-400">
              {streaming ? "⏳ 思考中…" : "🧠 思考过程"}
            </summary>
            <div className="mt-1.5 whitespace-pre-wrap border-l-2 border-orange-300/60 pl-2.5 text-xs text-stone-400">
              {msg.thinking}
            </div>
          </details>
        )}
        {msg.content ? (
          <div className="text-[15px] leading-relaxed [&_h1]:text-base [&_h1]:font-bold [&_h2]:text-base [&_h2]:font-bold [&_h3]:font-semibold [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-1 [&_strong]:font-semibold [&_ul]:list-disc [&_ul]:pl-5">
            <Markdown>{msg.content}</Markdown>
          </div>
        ) : (
          !msg.thinking && <TypingDots />
        )}
      </div>
    </div>
  );
}

function TypingDots() {
  return (
    <div className="flex gap-1 py-1.5">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className="h-1.5 w-1.5 animate-pulse rounded-full bg-stone-400"
          style={{ animationDelay: `${i * 0.18}s` }}
        />
      ))}
    </div>
  );
}

function Spinner() {
  return (
    <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
  );
}
