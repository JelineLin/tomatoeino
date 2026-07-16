// api.ts —— 和 Go 后端通信的唯一出口（对齐 iOS 的 APIClient：一处收敛「怎么连后端」）。
//
// 同源直调：网页由同一个 Go 服务托管（或 dev 模式下由 next dev 反代），
// 所有请求都是相对路径 /api/*，不存在跨域。token 存 localStorage，
// 所有请求统一从 authedFetch 出去；任何 401 都会触发「回到输码门」。

// ---- 类型：和 Go 后端 JSON 一一对应（字段全小写，见 internal/menu） ----

export interface Feedback {
  rating: string; // like / dislike / ok
  note?: string;
}

export interface Dish {
  name: string;
  detail: string;
  feedback?: Feedback | null; // 菜级反馈；旧数据缺字段
}

export interface Meal {
  time: string;
  dishes: Dish[];
  feedback?: Feedback | null; // 旧的餐级反馈：只读展示
}

export interface Day {
  date: string;
  lunch?: Meal | null;
  fruit?: Meal | null;
  dinner?: Meal | null;
}

export interface Season {
  month: number;
  veg: string[];
  fruit: string[];
  aquatic: string[];
  tip: string;
  source?: string;
}

export interface PrefRule {
  name: string;
  likes: number;
  dislikes: number;
  oks: number;
  advice: string;
}

export interface Profile {
  babyName?: string;
  birthDate?: string;
  allergies?: string[];
  dislikes?: string[];
  notes?: string;
  rules?: PrefRule[]; // 只读派生：后端归纳的偏好规律
}

export interface InventoryItem {
  name: string;
  quantity: number;
  unit: string;
}

export interface EditDish {
  name: string;
  detail: string;
}

export interface ProposedMeal {
  meal: string; // lunch/fruit/dinner
  time: string;
  dishes: EditDish[];
  reason: string;
  applied?: boolean;
}

export interface RecommendedMenu {
  date: string;
  meals: ProposedMeal[];
}

export interface DailyBrief {
  date: string;
  content: string;
  menu?: RecommendedMenu | null;
  generatedAt: string;
}

// ---- token 管理 ----

const TOKEN_KEY = "menuagent_token";

export function getToken(): string {
  if (typeof window === "undefined") return "";
  return localStorage.getItem(TOKEN_KEY) ?? "";
}

export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

// 401 时通知外壳「回到输码门」。用事件而不是状态库——这个 app 不值得引状态库。
export const UNAUTHORIZED_EVENT = "menuagent:unauthorized";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function authedFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${getToken()}`);
  if (init.body) headers.set("Content-Type", "application/json");
  const resp = await fetch(path, { ...init, headers });
  if (resp.status === 401) {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    throw new ApiError(401, "token 无效或已过期");
  }
  if (!resp.ok) {
    // 后端错误正文是给人看的中文（如「菜名不存在」），原样透传给界面。
    const text = (await resp.text()).trim();
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
  return resp;
}

async function getJSON<T>(path: string): Promise<T> {
  return (await authedFetch(path)).json();
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  return (await authedFetch(path, { method: "POST", body: JSON.stringify(body) })).json();
}

// ---- 各功能 API（对齐 iOS APIClient 的方法清单） ----

export const api = {
  history: () => getJSON<Day[]>("/api/history"),

  // 菜级反馈：dish 带菜名；rating 空串 = 清除。后端返回更新后的整份历史。
  submitFeedback: (date: string, meal: string, dish: string, rating: string, note: string) =>
    postJSON<Day[]>("/api/history/feedback", { date, meal, dish, rating, note }),

  seasonal: (month?: number) =>
    getJSON<Season>(`/api/seasonal${month ? `?month=${month}` : ""}`),

  profile: () => getJSON<Profile>("/api/profile"),
  updateProfile: (p: Profile) => postJSON<Profile>("/api/profile", p),

  inventory: () => getJSON<InventoryItem[]>("/api/inventory"),
  inventoryWrite: (op: "add" | "set" | "remove", name: string, quantity: number, unit: string) =>
    postJSON<InventoryItem[]>("/api/inventory", { op, name, quantity, unit }),

  brief: (refresh = false) =>
    getJSON<DailyBrief>(`/api/brief${refresh ? "?refresh=1" : ""}`),

  applyMeal: (date: string, meal: string, time: string, dishes: EditDish[]) =>
    postJSON<Day[]>("/api/history/apply", { date, meal, time, dishes }),

  parseOrderImage: (imageBase64: string, mime: string) =>
    postJSON<{ name: string; quantity: number; unit: string }[]>(
      "/api/inventory/parse-image", { image_base64: imageBase64, mime }),

  parseHistoryText: (text: string) =>
    postJSON<Day[]>("/api/history/parse", { text }),
  parseHistoryImage: (imageBase64: string, mime: string) =>
    postJSON<Day[]>("/api/history/parse", { image_base64: imageBase64, mime }),
  importHistory: (days: Day[]) =>
    postJSON<{ added: number; replaced: number; history: Day[] }>("/api/history/import", { days }),
};

// ---- 聊天：POST + SSE 流（协议与 iOS streamChat 完全一致） ----

export interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  context?: string; // 工具备忘（L1 回灌）：流末尾收到、下一轮随历史带回
}

export type ChatEvent =
  | { type: "thinking"; text: string }
  | { type: "answer"; text: string }
  | { type: "context"; text: string }
  | { type: "session"; text: string };

// streamChat 逐事件产出。协议：每段 `data: {json}\n\n`；`data: [DONE]` 结束；
// `data: [ERROR]说明` 报错（抛异常，由调用方接住展示）。
export async function* streamChat(
  messages: ChatMessage[],
  sessionID: string,
): AsyncGenerator<ChatEvent> {
  const resp = await authedFetch("/api/chat", {
    method: "POST",
    body: JSON.stringify({
      session_id: sessionID || undefined,
      messages: messages.map((m) => ({
        role: m.role,
        content: m.content,
        context: m.context || undefined,
      })),
    }),
  });
  if (!resp.body) throw new ApiError(0, "浏览器不支持流式读取");

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      // SSE 事件以空行分隔；最后一段可能不完整，留在 buf 里等下一块。
      for (;;) {
        const sep = buf.indexOf("\n\n");
        if (sep < 0) break;
        const chunk = buf.slice(0, sep);
        buf = buf.slice(sep + 2);
        for (const line of chunk.split("\n")) {
          if (!line.startsWith("data: ")) continue;
          const payload = line.slice(6);
          if (payload === "[DONE]") return;
          if (payload.startsWith("[ERROR]")) throw new ApiError(0, payload.slice(7));
          yield JSON.parse(payload) as ChatEvent;
        }
      }
    }
  } finally {
    reader.cancel().catch(() => {});
  }
}
