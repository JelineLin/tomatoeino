package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tomato-platform/internal/english"
	"tomato-platform/internal/platformauth"
)

type fakeAccountResolver struct {
	id  string
	err error
}

func (f fakeAccountResolver) Resolve(context.Context, string) (string, error) { return f.id, f.err }

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
	h := withAuth(reg, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestAuthAcceptsPlatformUserAndChecksMembership(t *testing.T) {
	const userID = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"
	handler := withAuth(nil, fakeAccountResolver{id: userID}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(userIDFrom(r)))
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/english/today", nil)
	request.Header.Set("Authorization", "Bearer platform-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != userID {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	denied := withAuth(nil, fakeAccountResolver{err: platformauth.ErrForbidden}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("denied membership reached handler")
	}))
	response = httptest.NewRecorder()
	denied.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("inactive membership status = %d", response.Code)
	}

	// 上游若返回带路径片段的 ID，绝不能让它拼进音频落盘目录。
	unsafe := withAuth(nil, fakeAccountResolver{id: "../../etc"}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsafe user ID reached handler")
	}))
	response = httptest.NewRecorder()
	unsafe.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("unsafe user ID status = %d", response.Code)
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
		Questions: []english.Question{{ID: "q1", Answer: "A", Explain: "Because A matches the passage."}},
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
	if len(got.Review) != 1 || got.Review[0].CorrectAnswer != "A" || !got.Review[0].IsCorrect || got.Review[0].Explanation == "" {
		t.Fatalf("已提交答案没有返回逐题解析: %+v", got.Review)
	}

	bobReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/english/answers?lesson_id=%d", lesson.ID), nil)
	bobReq = bobReq.WithContext(context.WithValue(bobReq.Context(), userKey{}, "bob"))
	bobResponse := httptest.NewRecorder()
	s.handleAnswers(bobResponse, bobReq)
	if bobResponse.Code != http.StatusNotFound {
		t.Fatalf("其他用户不应读取答案, status=%d body=%s", bobResponse.Code, bobResponse.Body.String())
	}
}

func TestHandleAnswersPostReturnsReviewWithoutChangingPublicLesson(t *testing.T) {
	store, err := english.OpenStore(filepath.Join(t.TempDir(), "learning.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	lesson, _, err := store.PutLesson(context.Background(), english.Lesson{
		UserID: "alice", Date: "2026-08-08", Title: "T", Passage: "Text",
		Questions: []english.Question{{ID: "q1", Answer: "B", Explain: "文中第二段直接说明。"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := &server{store: store}
	body, _ := json.Marshal(map[string]any{
		"lesson_id": lesson.ID,
		"answers":   []english.Answer{{QuestionID: "q1", Value: "A"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/english/answers", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), userKey{}, "alice"))
	response := httptest.NewRecorder()
	s.handleAnswers(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got english.ReadingAttempt
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Review) != 1 || got.Review[0].CorrectAnswer != "B" || got.Review[0].IsCorrect || got.Review[0].Explanation == "" {
		t.Fatalf("提交后解析错误: %+v", got.Review)
	}
	lessonReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/english/lessons/%d", lesson.ID), nil)
	lessonReq = lessonReq.WithContext(context.WithValue(lessonReq.Context(), userKey{}, "alice"))
	lessonResponse := httptest.NewRecorder()
	s.handleLesson(lessonResponse, lessonReq)
	if lessonResponse.Code != http.StatusOK {
		t.Fatalf("lesson status=%d body=%s", lessonResponse.Code, lessonResponse.Body.String())
	}
	var public english.Lesson
	if err := json.NewDecoder(lessonResponse.Body).Decode(&public); err != nil {
		t.Fatal(err)
	}
	if public.Questions[0].Answer != "" || public.Questions[0].Explain != "" {
		t.Fatalf("课程 API 泄露答案: %+v", public.Questions[0])
	}
}

// 与 Menu 同一个坑：未配置统一账户时必须是接口零值，否则错 token 会打崩请求。
func TestNewAccountResolverStaysNilInterfaceWhenUnconfigured(t *testing.T) {
	resolver, err := newAccountResolver("  ", "english")
	if err != nil {
		t.Fatal(err)
	}
	if resolver != nil {
		t.Fatalf("未配置统一账户时应是接口零值，得到 %#v", resolver)
	}
	if configured, err := newAccountResolver("http://127.0.0.1:8460", "english"); err != nil || configured == nil {
		t.Fatalf("配置后应返回可用解析器: %v", err)
	}
	if _, err := newAccountResolver("not-a-url", "english"); err == nil {
		t.Error("非法 ACCOUNT_BASE_URL 应在启动时报错")
	}

	users := &userRegistry{byHash: map[string]string{hashToken("secret"): "alice"}, names: map[string]string{"alice": "A"}}
	handler := withAuth(users, resolver, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("错 token 不该进 handler")
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/english/today", nil)
	request.Header.Set("Authorization", "Bearer wrong-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("老部署的错 token 应 401，得到 %d", response.Code)
	}
}
