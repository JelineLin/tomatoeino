package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tomatoeino/internal/english"
)

func TestAudioExtUsesMagicNotClientName(t *testing.T) {
	cases := []struct {
		b    []byte
		want string
	}{{[]byte{'O', 'g', 'g', 'S'}, ".ogg"}, {[]byte{0x1a, 0x45, 0xdf, 0xa3}, ".webm"}, {[]byte("RIFFxxxxWAVE"), ".wav"}, {[]byte{0, 0, 0, 8, 'f', 't', 'y', 'p'}, ".m4a"}}
	for _, tc := range cases {
		got, ok := audioExt(tc.b, "application/octet-stream")
		if !ok || got != tc.want {
			t.Fatalf("magic %x => %q,%v", tc.b, got, ok)
		}
	}
	if _, ok := audioExt([]byte("not audio"), "audio/webm"); ok {
		t.Fatal("只信 MIME 会放过伪造文件")
	}
}

func TestSPAHandlerCannotEscapeWebRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("safe shell"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	r := httptest.NewRequest(http.MethodGet, "http://example.test/../secret.txt", nil)
	w := httptest.NewRecorder()
	spaHandler(root).ServeHTTP(w, r)
	if w.Body.String() == "secret" {
		t.Fatal("静态文件处理器允许目录穿越")
	}
}

func TestAuthSeparatesEnglishAPIFromHealth(t *testing.T) {
	reg := &userRegistry{byHash: map[string]string{hashToken("secret"): "alice"}, names: map[string]string{"alice": "A"}}
	h := withAuth(reg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && userIDFrom(r) != "alice" {
			t.Errorf("身份未注入")
		}
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("health=%d", w.Code)
	}
	req = httptest.NewRequest("GET", "/api/english/today", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("无 token=%d", w.Code)
	}
	req = httptest.NewRequest("GET", "/api/english/today", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("有效 token=%d", w.Code)
	}
}

func TestHandleAnswersRestoresLatestSubmission(t *testing.T) {
	store, err := english.OpenStore(filepath.Join(t.TempDir(), "learning.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	lesson, _, err := store.PutLesson(context.Background(), english.Lesson{
		UserID: "alice", Date: "2026-08-07", Title: "T", Passage: "Text",
		Questions: []english.Question{{ID: "q1", Answer: "A"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := store.SaveReadingAttempt(context.Background(), "alice", lesson.ID, []english.Answer{{QuestionID: "q1", Value: "A"}})
	if err != nil {
		t.Fatal(err)
	}

	s := &server{store: store}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/english/answers?lesson_id=%d", lesson.ID), nil)
	req = req.WithContext(context.WithValue(req.Context(), userKey{}, "alice"))
	response := httptest.NewRecorder()
	s.handleAnswers(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got english.ReadingAttempt
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || len(got.Answers) != 1 || got.Answers[0].Value != "A" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
