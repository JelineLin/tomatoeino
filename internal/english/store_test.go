package english

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "learning.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrationAndProfileAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learning.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.EnsureProfile(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if p.Level != "B1" || p.CET4Score != 430 || p.Navigation != "bottom" {
		t.Fatalf("默认档案异常: %+v", p)
	}
	_ = s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p2, err := s.EnsureProfile(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if p2.UserID != "alice" {
		t.Fatalf("重开后档案丢失: %+v", p2)
	}
}

func TestMigrationAddsNavigationToExistingProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE learning_profiles (
 user_id TEXT PRIMARY KEY, cet4_score INTEGER NOT NULL, level TEXT NOT NULL,
 daily_minutes INTEGER NOT NULL, goal TEXT NOT NULL, difficulty INTEGER NOT NULL, updated_at TEXT NOT NULL);
 INSERT INTO learning_profiles VALUES ('alice', 430, 'B1', 45, 'read more', 2, '2026-08-10T00:00:00Z');`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.Profile(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if p.Navigation != DefaultNavigation || p.Difficulty != 2 {
		t.Fatalf("旧档案迁移异常: %+v", p)
	}
}

func TestProfileNavigationPosition(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, err := s.EnsureProfile(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	p.Navigation = "right"
	saved, err := s.SaveProfile(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Navigation != "right" {
		t.Fatalf("导航位置未保存: %+v", saved)
	}
	p.Navigation = "top"
	if _, err := s.SaveProfile(ctx, p); err == nil {
		t.Fatal("非法导航位置应被拒绝")
	}
}

type fakeGenerator struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeGenerator) Generate(_ context.Context, p Profile, _ ProgressSnapshot, day time.Time, _ []string) (Lesson, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return Lesson{UserID: p.UserID, Date: day.Format("2006-01-02"), Title: "A Small Step", Passage: "Practice makes progress every day.", Questions: []Question{{ID: "q1", Prompt: "?", Options: []string{"A", "B"}, Answer: "A"}}, Difficulty: p.Difficulty}, nil
}

func TestGenerateTodayConcurrentIdempotency(t *testing.T) {
	s := testStore(t)
	g := &fakeGenerator{}
	day := time.Date(2026, 8, 7, 8, 0, 0, 0, time.Local)
	ctx := context.Background()
	const workers = 8
	ids := make(chan int64, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, _, err := GenerateToday(ctx, s, g, "alice", day)
			if err != nil {
				errs <- err
				return
			}
			ids <- l.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first int64
	count := 0
	for id := range ids {
		count++
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("同一天生成了不同课程: %d / %d", first, id)
		}
	}
	if count != workers {
		t.Fatalf("只返回 %d 份结果", count)
	}
	lessons, err := s.Lessons(ctx, "alice", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 {
		t.Fatalf("数据库里应只有一课，得到 %d", len(lessons))
	}
}

func TestUserIsolationAndReadingScore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	l, _, err := s.PutLesson(ctx, Lesson{UserID: "alice", Date: "2026-08-07", Title: "T", Passage: "Text", Questions: []Question{{ID: "q1", Answer: "A"}, {ID: "q2", Answer: "B"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lesson(ctx, "bob", l.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob 不应读到 alice 的课: %v", err)
	}
	a, err := s.SaveReadingAttempt(ctx, "alice", l.ID, []Answer{{QuestionID: "q1", Value: "a"}, {QuestionID: "q2", Value: "C"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Correct != 1 || a.Accuracy != .5 {
		t.Fatalf("评分错误: %+v", a)
	}
	latest, err := s.LatestReadingAttempt(ctx, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != a.ID || len(latest.Answers) != 2 || latest.Answers[1].Value != "C" {
		t.Fatalf("未恢复最近一次作答: %+v", latest)
	}
	if _, err := s.LatestReadingAttempt(ctx, "bob", l.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob 不应读到 alice 的答案: %v", err)
	}
}

func TestLatestReadingAttemptUsesNewestSubmission(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	l, _, err := s.PutLesson(ctx, Lesson{UserID: "alice", Date: "2026-08-08", Title: "T", Passage: "Text", Questions: []Question{{ID: "q1", Answer: "A"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveReadingAttempt(ctx, "alice", l.ID, []Answer{{QuestionID: "q1", Value: "B"}}); err != nil {
		t.Fatal(err)
	}
	want, err := s.SaveReadingAttempt(ctx, "alice", l.ID, []Answer{{QuestionID: "q1", Value: "A"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestReadingAttempt(ctx, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Correct != 1 || got.Answers[0].Value != "A" {
		t.Fatalf("got %+v, want newest %+v", got, want)
	}
}

func TestPublicLessonHidesAnswers(t *testing.T) {
	l := Lesson{Questions: []Question{{ID: "q1", Answer: "A", Explain: "because"}}}
	public := PublicLesson(l)
	if public.Questions[0].Answer != "" || public.Questions[0].Explain != "" {
		t.Fatalf("客户端课程泄露答案: %+v", public.Questions[0])
	}
	if l.Questions[0].Answer != "A" {
		t.Fatal("脱敏不应修改账本原值")
	}
}

func TestWithReadingReviewUsesSubmittedAnswers(t *testing.T) {
	lesson := Lesson{Questions: []Question{
		{ID: "q1", Answer: "A", Explain: "第一题解析"},
		{ID: "q2", Answer: "C", Explain: "第二题解析"},
	}}
	attempt := ReadingAttempt{Answers: []Answer{
		{QuestionID: "q1", Value: "a"},
		{QuestionID: "q2", Value: "B"},
	}}
	got := WithReadingReview(attempt, lesson)
	if len(got.Review) != 2 || !got.Review[0].IsCorrect || got.Review[1].IsCorrect {
		t.Fatalf("逐题判断错误: %+v", got.Review)
	}
	if got.Review[1].CorrectAnswer != "C" || got.Review[1].Explanation != "第二题解析" {
		t.Fatalf("正确答案或解析缺失: %+v", got.Review[1])
	}
}

func TestNotificationCanOnlyBeClaimedOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	started, err := s.StartJob(ctx, "alice", "2026-08-07", "generate-today")
	if err != nil || !started {
		t.Fatalf("start=%v err=%v", started, err)
	}
	first, err := s.ClaimNotification(ctx, "alice", "2026-08-07", "generate-today")
	if err != nil || !first {
		t.Fatalf("first claim=%v err=%v", first, err)
	}
	second, err := s.ClaimNotification(ctx, "alice", "2026-08-07", "generate-today")
	if err != nil || second {
		t.Fatalf("通知不应重复 claim: %v err=%v", second, err)
	}
}
