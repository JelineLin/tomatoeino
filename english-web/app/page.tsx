"use client";

import { useEffect, useState } from "react";
import { ApiError, api, type Lesson, type ReadingAttempt } from "@/lib/api";
import { Empty, Header, Loading } from "@/components/UI";

export default function TodayPage() {
  const [lesson, setLesson] = useState<Lesson | null>(null);
  const [loading, setLoading] = useState(true);
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [attempt, setAttempt] = useState<ReadingAttempt | null>(null);

  useEffect(() => {
    let active = true;
    async function load() {
      try {
        const today = await api.today();
        if (!active) return;
        setLesson(today);
        try {
          const saved = await api.readingAttempt(today.id);
          if (!active) return;
          setAttempt(saved);
          setAnswers(Object.fromEntries(saved.answers.map((answer) => [answer.question_id, answer.value.toUpperCase()])));
        } catch (loadAttemptError) {
          if (!(loadAttemptError instanceof ApiError && loadAttemptError.status === 404)) throw loadAttemptError;
        }
      } catch (loadError) {
        if (!active) return;
        if (loadError instanceof ApiError && loadError.status === 404) setMissing(true);
        else setError(loadError instanceof Error ? loadError.message : String(loadError));
      } finally {
        if (active) setLoading(false);
      }
    }
    load();
    return () => { active = false; };
  }, []);

  async function generate() {
    setBusy(true);
    setError("");
    try {
      const data = await api.generateToday();
      setLesson(data.lesson);
      setMissing(false);
      setAnswers({});
      setAttempt(null);
    } catch (generateError) {
      setError(generateError instanceof Error ? generateError.message : String(generateError));
    } finally {
      setBusy(false);
    }
  }

  async function submit() {
    if (!lesson) return;
    setBusy(true);
    setError("");
    try {
      const data = await api.submitAnswers(
        lesson.id,
        lesson.questions.map((question) => ({ question_id: question.id, value: answers[question.id] ?? "" })),
      );
      setAttempt(data);
      setAnswers(Object.fromEntries(data.answers.map((answer) => [answer.question_id, answer.value.toUpperCase()])));
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-full pb-5">
      <Header title="今日课程" subtitle="B1 · CET-4 基础 · 30～60 分钟" />
      {loading ? <Loading /> : missing ? (
        <Empty>
          <div className="mb-3 text-4xl">🌤️</div>
          <p>今天的课程还没有生成</p>
          <button onClick={generate} disabled={busy} className="mt-5 rounded-2xl bg-indigo-600 px-6 py-3 font-semibold text-white disabled:bg-slate-300">
            {busy ? "生成中…" : "生成今日课程"}
          </button>
        </Empty>
      ) : lesson ? (
        <article className="space-y-4 px-4 py-5">
          <section className="rounded-3xl bg-gradient-to-br from-indigo-600 to-violet-600 p-5 text-white shadow-lg shadow-indigo-200">
            <div className="flex items-center justify-between text-xs text-indigo-100">
              <span>{lesson.date}</span>
              <span>难度 {lesson.difficulty}/5 · {lesson.estimated_minutes} 分钟</span>
            </div>
            <h2 className="mt-3 text-2xl font-bold leading-tight">{lesson.title}</h2>
            <p className="mt-2 text-xs leading-relaxed text-indigo-100">{lesson.generation_reason}</p>
          </section>

          {lesson.source_name && (
            <section className="rounded-3xl border border-indigo-100 bg-indigo-50 p-4">
              <div className="flex flex-wrap gap-2 text-[11px] font-semibold text-indigo-700">
                <span className="rounded-full bg-white px-2.5 py-1">{lesson.exercise_style || lesson.content_mode}</span>
                {lesson.source_published_at && <span className="rounded-full bg-white px-2.5 py-1">{lesson.source_published_at}</span>}
              </div>
              <div className="mt-3 text-xs text-slate-500">内容来源</div>
              <div className="mt-1 font-semibold text-indigo-950">{lesson.source_name}{lesson.source_title ? ` · ${lesson.source_title}` : ""}</div>
              {lesson.syllabus_focus && <p className="mt-2 text-sm text-slate-600">能力点：{lesson.syllabus_focus}</p>}
              {lesson.adaptation_note && <p className="mt-1 text-xs text-slate-500">{lesson.adaptation_note}</p>}
              {lesson.source_url && <a href={lesson.source_url} target="_blank" rel="noreferrer" className="mt-3 inline-block text-sm font-semibold text-indigo-600">查看原始来源 →</a>}
            </section>
          )}

          <section className="rounded-3xl bg-white p-5 shadow-sm">
            <h3 className="mb-4 font-bold text-indigo-950">Reading</h3>
            <p className="whitespace-pre-wrap font-serif text-[17px] leading-8 text-slate-800">{lesson.passage}</p>
          </section>

          <section className="rounded-3xl bg-white p-5 shadow-sm">
            <h3 className="mb-4 font-bold text-indigo-950">Key vocabulary</h3>
            <div className="space-y-4">
              {lesson.vocabulary.map((word) => (
                <div key={word.word} className="border-b border-slate-100 pb-3 last:border-0">
                  <div className="flex items-baseline gap-2"><strong className="text-indigo-700">{word.word}</strong><span className="text-xs text-slate-400">{word.pronunciation}</span></div>
                  <p className="text-sm text-slate-700">{word.meaning}</p>
                  <p className="mt-1 text-xs italic text-slate-400">{word.example}</p>
                </div>
              ))}
            </div>
          </section>

          <section className="rounded-3xl bg-white p-5 shadow-sm">
            <h3 className="mb-4 font-bold text-indigo-950">Quick check</h3>
            <div className="space-y-6">
              {lesson.questions.map((question, index) => {
                const review = attempt?.review?.find((item) => item.question_id === question.id);
                return (
                  <div key={question.id}>
                    {question.type && <span className="mb-1 inline-block rounded-full bg-slate-100 px-2 py-0.5 text-[10px] uppercase text-slate-500">{question.type.replaceAll("_", " ")}</span>}
                    <p className="mb-2 text-sm font-medium">{index + 1}. {question.prompt}</p>
                    <div className="grid gap-2">
                      {question.options.map((option, optionIndex) => {
                        const value = String.fromCharCode(65 + optionIndex);
                        const selected = answers[question.id] === value;
                        const isCorrectOption = review?.correct_answer.toUpperCase() === value;
                        const resultClass = isCorrectOption
                          ? "border-emerald-500 bg-emerald-50 text-emerald-900"
                          : review && selected
                            ? "border-red-400 bg-red-50 text-red-900"
                            : selected
                              ? "border-indigo-500 bg-indigo-50"
                              : "border-slate-200";
                        return (
                          <label key={option} className={`flex gap-3 rounded-xl border px-3 py-2.5 text-sm ${attempt ? "cursor-default" : "cursor-pointer"} ${resultClass}`}>
                            <input type="radio" name={question.id} checked={selected} disabled={attempt !== null} onChange={() => setAnswers((current) => ({ ...current, [question.id]: value }))} />
                            <span className="flex-1">{option}</span>
                            {isCorrectOption && <span aria-label="正确答案">✓</span>}
                            {review && selected && !review.is_correct && <span aria-label="你的错误答案">✕</span>}
                          </label>
                        );
                      })}
                    </div>
                    {review && (
                      <div className={`mt-3 rounded-xl p-3 text-sm ${review.is_correct ? "bg-emerald-50 text-emerald-800" : "bg-amber-50 text-amber-900"}`}>
                        <p className="font-semibold">{review.is_correct ? "回答正确" : `正确答案：${review.correct_answer}`}</p>
                        {review.explanation && <p className="mt-1 leading-6">解析：{review.explanation}</p>}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
            {attempt ? (
              <p className="mt-5 rounded-xl bg-emerald-50 p-3 text-center font-semibold text-emerald-700">答对 {attempt.correct}/{attempt.total}，正确率 {Math.round(attempt.accuracy * 100)}%</p>
            ) : (
              <button onClick={submit} disabled={busy || Object.keys(answers).length < lesson.questions.length} className="mt-5 w-full rounded-2xl bg-indigo-600 py-3 font-semibold text-white disabled:bg-slate-300">{busy ? "提交中…" : "提交答案"}</button>
            )}
          </section>
        </article>
      ) : null}
      {error && <p className="mx-5 mt-4 rounded-xl bg-red-50 p-3 text-sm text-red-600">{error}</p>}
    </div>
  );
}
