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

// IngredientUse 是「这道菜用掉哪样库存、用掉多少」。不带单位——单位以账本为准。
export interface IngredientUse {
  name: string;
  qty: number;
}

export interface Dish {
  name: string;
  detail: string;
  feedback?: Feedback | null; // 菜级反馈；旧数据缺字段
  uses?: IngredientUse[]; // 推荐时登记的库存消耗；历史里的旧菜没有
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

// 后端在 GET/写后响应里一并下发新鲜度（派生字段，服务端现算不落盘），
// 且已按「越该吃越靠前」排好序——前端照序渲染就是家长最该先处理的顺序。
// 新鲜度字段全部可选：旧后端不发时不该让库存页崩掉。
export interface InventoryItem {
  name: string;
  quantity: number;
  unit: string;
  updatedAt?: string; // 最近一次补货时刻（RFC3339）
  freshness?: "fresh" | "use_soon" | "stale" | "unknown";
  days?: number; // 放了几天；-1 = 不详
  shelfLife?: number; // 这个品类的保鲜期（天）
  category?: string;
}

// 新鲜度徽章：只有「该吃了」和「可能没了」值得占用视觉——全都标一遍等于没标。
export function freshnessBadge(it: InventoryItem): { text: string; urgent: boolean } | null {
  if (it.freshness === "use_soon") {
    return { text: it.days != null && it.days >= 0 ? `该吃了 · ${it.days}天` : "该吃了", urgent: false };
  }
  if (it.freshness === "stale") {
    return { text: it.days != null && it.days >= 0 ? `可能没了 · ${it.days}天` : "可能没了", urgent: true };
  }
  return null;
}

export interface EditDish {
  name: string;
  detail: string;
  // 这道菜吃掉的库存。采纳时原样回传给后端 → 自动出库。
  // 换备选菜时整个 EditDish 被替换，uses 跟着换，不用单独同步。
  uses?: IngredientUse[];
}

export interface ProposedMeal {
  meal: string; // lunch/fruit/dinner
  time: string;
  dishes: EditDish[];
  // 备选菜（后端约定每餐 2 道）：家长不满意主推时直接顶替，零请求零延迟。
  // 旧后端缺字段 → undefined，前端据此不显示换菜入口。
  alternatives?: EditDish[];
  reason: string;
  applied?: boolean;
}

// ConsumedItem 是采纳一餐后自动出库的一条结果，用来提示家长「刚扣了什么」。
export interface ConsumedItem {
  name: string;
  qty: number;
  unit: string;
  remaining: number;
  depleted: boolean;
}

// ReplaceDishResult 是 /api/brief/dish 的响应：新菜 + 换完的整份菜单 + agent 的一句说明。
// 直接用 menu 替换本地状态——服务端已经把简报缓存改好了，两边保持一致。
export interface ReplaceDishResult {
  dish: EditDish;
  menu: RecommendedMenu | null;
  note: string;
}

// ApplyResult 是 /api/history/apply 的响应：新历史 + 这次自动扣了什么。
export interface ApplyResult {
  history: Day[];
  consumed?: ConsumedItem[];
  missed?: string[];
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
  clearChatState(); // 换家庭/掉线回门口时，上一个人的对话不该留在这台机器上
}

// ---- 聊天状态持久化 ----
//
// 网页版和 iOS 的结构差异：底部导航是 Next 的 <Link>，切到「推荐」再切回来，
// 聊天页组件会被【卸载重建】——存在组件里的消息和会话钥匙一起归零，
// 于是每次切 tab 回来 agent 都像第一次见到你。iOS 没这毛病是因为 TabView
// 把页面留在内存里。修法：把这两样落到 localStorage，和 token 同一套路。
//
// 会话钥匙存下来是安全的：服务端会话有 30 分钟 TTL，过期后这把钥匙命不中，
// 后端自动退回 L1 用我们一并存下的全量消息重建上下文——过期只是少省点 token，
// 对话不会断。

const CHAT_KEY = "menuagent_chat";

// 只留最近 N 条气泡。localStorage 有约 5MB 上限，而思考过程很占地方；
// 家庭场景翻不到那么早的对话，超出的直接丢。
const CHAT_MAX_BUBBLES = 40;

export interface PersistedChat<T> {
  sessionID: string;
  messages: T[];
}

export function loadChatState<T>(): PersistedChat<T> | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = localStorage.getItem(CHAT_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as PersistedChat<T>;
    if (!Array.isArray(v?.messages)) return null;
    return { sessionID: typeof v.sessionID === "string" ? v.sessionID : "", messages: v.messages };
  } catch {
    return null; // 存坏了就当没有，不能让一条脏数据把聊天页整个打不开
  }
}

export function saveChatState<T>(sessionID: string, messages: T[]) {
  if (typeof window === "undefined") return;
  try {
    const trimmed = messages.slice(-CHAT_MAX_BUBBLES);
    localStorage.setItem(CHAT_KEY, JSON.stringify({ sessionID, messages: trimmed }));
  } catch {
    // 写满了/隐私模式禁写：放弃持久化即可，内存里的对话照常进行
  }
}

export function clearChatState() {
  if (typeof window === "undefined") return;
  try {
    localStorage.removeItem(CHAT_KEY);
  } catch {
    /* 忽略 */
  }
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
  // 批量入库：一句话/一张订单解析出来的通常是好几样，逐条发请求会在中途失败时
  // 留下「入了一半」的账。后端 add_batch 先全校验再全写入。
  inventoryAddBatch: (items: InventoryItem[]) =>
    postJSON<InventoryItem[]>("/api/inventory", { op: "add_batch", items }),

  brief: (refresh = false) =>
    getJSON<DailyBrief>(`/api/brief${refresh ? "?refresh=1" : ""}`),

  // 只换某一餐的第 dishIndex 道菜，其余原样。「调整菜单」的第二级——
  // 备选菜（零请求）是第一级，两道都不满意才落到这里；再不行才是整份重生成。
  replaceDish: (meal: string, dishIndex: number, instruction = "") =>
    postJSON<ReplaceDishResult>("/api/brief/dish", { meal, dishIndex, instruction }),

  // 返回值除了新历史还带「这次自动扣了什么库存」——采纳即出库，但扣了什么
  // 必须当场说清楚，不然就是背着人改账本。
  applyMeal: (date: string, meal: string, time: string, dishes: EditDish[]) =>
    postJSON<ApplyResult>("/api/history/apply", { date, meal, time, dishes }),

  parseOrderImage: (imageBase64: string, mime: string) =>
    postJSON<InventoryItem[]>(
      "/api/inventory/parse-image", { image_base64: imageBase64, mime }),
  // 一句话入库：家长打/说一句「买了两块鳕鱼、一个西兰花」，后端解析成条目（不入库）。
  parseInventoryText: (text: string) =>
    postJSON<InventoryItem[]>("/api/inventory/parse-text", { text }),

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
