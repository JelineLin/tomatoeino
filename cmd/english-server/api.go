package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tomato-platform/internal/english"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, english.ErrNotFound) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
func only(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (s *server) handleToday(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodGet) {
		return
	}
	l, err := s.store.LessonByDate(r.Context(), userIDFrom(r), time.Now().Format("2006-01-02"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, english.PublicLesson(l))
}
func (s *server) handleLessons(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodGet) {
		return
	}
	v, err := s.store.Lessons(r.Context(), userIDFrom(r), 30)
	if err != nil {
		writeError(w, err)
		return
	}
	for i := range v {
		v[i] = english.PublicLesson(v[i])
	}
	writeJSON(w, 200, v)
}
func (s *server) handleLesson(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodGet) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/english/lessons/"), 10, 64)
	if err != nil {
		http.Error(w, "课程 ID 无效", 400)
		return
	}
	v, err := s.store.Lesson(r.Context(), userIDFrom(r), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, english.PublicLesson(v))
}

func (s *server) handleAnswers(w http.ResponseWriter, r *http.Request) {
	userID := userIDFrom(r)
	if r.Method == http.MethodGet {
		lessonID, err := strconv.ParseInt(r.URL.Query().Get("lesson_id"), 10, 64)
		if err != nil || lessonID <= 0 {
			http.Error(w, "lesson_id 无效", http.StatusBadRequest)
			return
		}
		v, err := s.store.LatestReadingAttempt(r.Context(), userID, lessonID)
		if err != nil {
			writeError(w, err)
			return
		}
		v, err = s.withReadingReview(r.Context(), userID, v)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	if !only(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		LessonID int64            `json:"lesson_id"`
		Answers  []english.Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON 无效", 400)
		return
	}
	v, err := s.store.SaveReadingAttempt(r.Context(), userID, req.LessonID, req.Answers)
	if err != nil {
		writeError(w, err)
		return
	}
	v, err = s.withReadingReview(r.Context(), userID, v)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}

func (s *server) withReadingReview(ctx context.Context, userID string, attempt english.ReadingAttempt) (english.ReadingAttempt, error) {
	lesson, err := s.store.Lesson(ctx, userID, attempt.LessonID)
	if err != nil {
		return english.ReadingAttempt{}, err
	}
	return english.WithReadingReview(attempt, lesson), nil
}

func (s *server) handleProgress(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodGet) {
		return
	}
	v, err := s.store.Progress(r.Context(), userIDFrom(r), time.Now())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *server) handleReports(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodGet) {
		return
	}
	v, err := s.store.WeeklyReports(r.Context(), userIDFrom(r), 12)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) handleProfile(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if r.Method == http.MethodGet {
		v, err := s.store.EnsureProfile(r.Context(), uid)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var p english.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "JSON 无效", 400)
		return
	}
	p.UserID = uid
	v, err := s.store.SaveProfile(r.Context(), p)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) handleGenerateToday(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodPost) {
		return
	}
	now := time.Now()
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		http.Error(w, "周末不自动生成课程", 409)
		return
	}
	l, created, err := english.GenerateToday(r.Context(), s.store, s.generator, userIDFrom(r), now)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[bool]int{true: 201, false: 200}[created], map[string]any{"lesson": english.PublicLesson(l), "created": created})
}

func (s *server) handleAttempts(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, "录音超过 20MB 或表单无效", 400)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	lessonID, err := strconv.ParseInt(r.FormValue("lesson_id"), 10, 64)
	if err != nil {
		http.Error(w, "lesson_id 无效", 400)
		return
	}
	duration, _ := strconv.ParseFloat(r.FormValue("duration_seconds"), 64)
	if duration <= 0 || duration > 600 {
		http.Error(w, "录音时长需在 0～600 秒", 400)
		return
	}
	lesson, err := s.store.Lesson(r.Context(), userIDFrom(r), lessonID)
	if err != nil {
		writeError(w, err)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "缺少 audio 文件", 400)
		return
	}
	defer file.Close()
	path, err := s.saveAudio(userIDFrom(r), file, header)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	normalized, actualDuration, normalizeErr := english.NormalizeAudio(r.Context(), path)
	if errors.Is(normalizeErr, english.ErrAudioTooLong) {
		_ = os.Remove(path)
		http.Error(w, english.ErrAudioTooLong.Error(), http.StatusBadRequest)
		return
	}
	var transcript english.Transcript
	var asrErr error
	if normalizeErr != nil {
		asrErr = normalizeErr
	} else {
		defer os.Remove(normalized)
		transcript, asrErr = s.speech.Transcribe(r.Context(), normalized)
		duration = actualDuration
	}
	a := english.SpeakingAttempt{UserID: userIDFrom(r), LessonID: lessonID, AudioPath: path, DurationSec: duration, Transcript: transcript.Text}
	if asrErr != nil {
		a.Feedback = asrErr.Error()
	} else {
		a.Accuracy, a.WPM, a.Score, a.Issues = english.EvaluateTranscript(lesson.Passage, transcript.Text, duration)
		a.Feedback = feedbackFor(a)
		profile, profileErr := s.store.EnsureProfile(r.Context(), a.UserID)
		progress, progressErr := s.store.Progress(r.Context(), a.UserID, time.Now())
		if profileErr == nil && progressErr == nil && s.coach != nil {
			if feedback, coachErr := s.coach.Feedback(r.Context(), profile, progress, a); coachErr == nil {
				a.Feedback = feedback
			}
		}
	}
	a, err = s.store.SaveSpeakingAttempt(r.Context(), a)
	if err != nil {
		_ = os.Remove(path)
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if asrErr != nil {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"attempt": a, "degraded": asrErr != nil})
}

func feedbackFor(a english.SpeakingAttempt) string {
	if a.Accuracy >= .9 {
		return "内容跟读很准确，下一次重点练习句子重音和自然停顿。"
	}
	if a.Accuracy >= .75 {
		return fmt.Sprintf("整体不错，有 %d 处单词需要慢速重读。", len(a.Issues))
	}
	return "先降速逐句朗读，优先把漏读和错读单词读清楚，再尝试连贯表达。"
}

func (s *server) handleAttempt(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, http.MethodGet) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/english/attempts/"), 10, 64)
	if err != nil {
		http.Error(w, "练习 ID 无效", 400)
		return
	}
	v, err := s.store.SpeakingAttempt(r.Context(), userIDFrom(r), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) saveAudio(uid string, file multipart.File, header *multipart.FileHeader) (string, error) {
	first := make([]byte, 16)
	n, err := io.ReadFull(file, first)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	first = first[:n]
	ext, ok := audioExt(first, header.Header.Get("Content-Type"))
	if !ok {
		return "", fmt.Errorf("只支持 webm、ogg、mp4/m4a 或 wav 录音")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	day := time.Now()
	dir := filepath.Join(s.audioDir, uid, day.Format("2006"), day.Format("01"), day.Format("02"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	path := filepath.Join(dir, hex.EncodeToString(b)+ext)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	return path, nil
}

func audioExt(b []byte, mime string) (string, bool) {
	if len(b) >= 4 && string(b[:4]) == "OggS" {
		return ".ogg", true
	}
	if len(b) >= 4 && b[0] == 0x1a && b[1] == 0x45 && b[2] == 0xdf && b[3] == 0xa3 {
		return ".webm", true
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE" {
		return ".wav", true
	}
	if len(b) >= 8 && string(b[4:8]) == "ftyp" {
		return ".m4a", true
	}
	_ = mime
	return "", false
}
