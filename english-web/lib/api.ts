export interface Vocabulary { phrase?: string; phrase_meaning?: string; word: string; meaning: string; example: string; pronunciation?: string }
export interface Question { id: string; type?: string; prompt: string; options: string[]; explanation?: string }
export interface Lesson { id: number; date: string; title: string; passage: string; vocabulary: Vocabulary[]; questions: Question[]; difficulty: number; generation_reason: string; estimated_minutes: number; content_mode: string; exercise_style: string; syllabus_focus: string; source_name: string; source_title: string; source_url: string; source_published_at: string; adaptation_note: string }
export interface ReadingAnswer { question_id: string; value: string }
export interface ReadingReview { question_id: string; correct_answer: string; is_correct: boolean; explanation: string }
export interface ReadingAttempt { id: number; lesson_id: number; answers: ReadingAnswer[]; review?: ReadingReview[]; correct: number; total: number; accuracy: number; completed_at: string }
export interface WordIssue { expected: string; actual?: string; kind: string }
export interface SpeakingAttempt { id: number; lesson_id: number; duration_seconds: number; transcript: string; wpm: number; accuracy: number; score: number; feedback: string; issues: WordIssue[] }
export interface Progress { level: string; difficulty: number; completion_rate_4w: number; reading_accuracy_4w: number; speaking_accuracy_4w: number; speaking_speed_wpm: number; recurring_errors: string[]; lessons_assigned: number; lessons_completed: number; recommended_difficulty: number }
export type NavigationPosition = "bottom" | "left" | "right";
export type ContentMode = "balanced" | "new-concept" | "ielts" | "china-daily" | "tech";
export interface Profile { user_id?: string; cet4_score: number; level: string; daily_minutes: number; goal: string; difficulty: number; navigation_position: NavigationPosition; content_mode: ContentMode; ielts_track: "academic" | "general" }
export interface WeeklyReport { id: number; week_start: string; completion_rate: number; reading_accuracy: number; speaking_wpm: number; problem_words: string[]; summary: string; next_focus: string }

const TOKEN_KEY = "englishcoach_token";
export const UNAUTHORIZED_EVENT = "englishcoach:unauthorized";
export const NAVIGATION_EVENT = "englishcoach:navigation-position";
export const getToken = () => typeof window === "undefined" ? "" : localStorage.getItem(TOKEN_KEY) ?? "";
export const setToken = (v: string) => localStorage.setItem(TOKEN_KEY, v);
export const clearToken = () => localStorage.removeItem(TOKEN_KEY);

export class ApiError extends Error { constructor(public status: number, message: string) { super(message) } }

async function request(path: string, init: RequestInit = {}) {
  const headers = new Headers(init.headers); headers.set("Authorization", `Bearer ${getToken()}`);
  if (init.body && !(init.body instanceof FormData)) headers.set("Content-Type", "application/json");
  const response = await fetch(path, { ...init, headers });
  if (response.status === 401) { window.dispatchEvent(new Event(UNAUTHORIZED_EVENT)); throw new ApiError(401, "访问码已失效") }
  if (!response.ok) throw new ApiError(response.status, (await response.text()).trim() || `HTTP ${response.status}`);
  return response;
}

const get = async <T,>(path: string): Promise<T> => (await request(path)).json();
const post = async <T,>(path: string, body?: unknown): Promise<T> => (await request(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) })).json();

export const api = {
  today: () => get<Lesson>("/api/english/today"),
  lessons: () => get<Lesson[]>("/api/english/lessons"),
  generateToday: () => post<{ lesson: Lesson; created: boolean }>("/api/english/generate-today"),
  readingAttempt: (lessonID: number) => get<ReadingAttempt>(`/api/english/answers?lesson_id=${lessonID}`),
  submitAnswers: (lessonID: number, answers: ReadingAnswer[]) => post<ReadingAttempt>("/api/english/answers", { lesson_id: lessonID, answers }),
  progress: () => get<Progress>("/api/english/progress"),
  reports: () => get<WeeklyReport[]>("/api/english/weekly-reports"),
  profile: () => get<Profile>("/api/english/profile"),
  saveProfile: (profile: Profile) => post<Profile>("/api/english/profile", profile),
  uploadAttempt: async (lessonID: number, audio: Blob, duration: number) => {
    const data = new FormData(); data.set("lesson_id", String(lessonID)); data.set("duration_seconds", String(duration)); data.set("audio", audio, `reading.${audio.type.includes("mp4") ? "m4a" : audio.type.includes("ogg") ? "ogg" : "webm"}`);
    return (await request("/api/english/attempts", { method: "POST", body: data })).json() as Promise<{ attempt: SpeakingAttempt; degraded: boolean }>;
  },
};
