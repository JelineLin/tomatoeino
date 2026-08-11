package english

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestBalancedContentSchedule(t *testing.T) {
	profile := Profile{ContentMode: ModeBalanced}
	want := map[time.Weekday]string{
		time.Monday: ModeNewConcept, time.Tuesday: ModeChinaDaily, time.Wednesday: ModeTech,
		time.Thursday: ModeIELTS, time.Friday: ModeWeeklyReview,
	}
	for weekday, mode := range want {
		day := time.Date(2026, 8, 10+int(weekday-time.Monday), 8, 0, 0, 0, time.Local)
		if got := contentModeForDay(profile, day); got != mode {
			t.Fatalf("%s 得到 %s，想要 %s", weekday, got, mode)
		}
	}
	profile.ContentMode = ModeTech
	if got := contentModeForDay(profile, time.Date(2026, 8, 10, 8, 0, 0, 0, time.Local)); got != ModeTech {
		t.Fatalf("固定内容模式未生效: %s", got)
	}
}

func TestProfileAndLessonSourceMetadataRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, err := s.EnsureProfile(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if p.ContentMode != DefaultContentMode || p.IELTSTrack != DefaultIELTSTrack {
		t.Fatalf("默认课程配置异常: %+v", p)
	}
	p.ContentMode = ModeIELTS
	p.IELTSTrack = "general"
	if _, err := s.SaveProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	l, _, err := s.PutLesson(ctx, Lesson{
		UserID: "alice", Date: "2026-08-11", Title: "News", Passage: "Text", Difficulty: 2,
		ContentMode: ModeChinaDaily, ExerciseStyle: "新闻英语", SyllabusFocus: "事实细节",
		SourceName: "China Daily", SourceTitle: "A headline", SourceURL: "https://www.chinadaily.com.cn/a/example.html",
		SourcePublished: "2026-08-11", AdaptationNote: "原创改写", ContentHash: "hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.SourceName != "China Daily" || l.SourcePublished != "2026-08-11" || l.ContentMode != ModeChinaDaily {
		t.Fatalf("来源元数据未完整恢复: %+v", l)
	}
	urls, err := s.RecentSourceURLs(ctx, "alice", 10)
	if err != nil || len(urls) != 1 || urls[0] != l.SourceURL {
		t.Fatalf("来源去重查询异常: %v / %#v", err, urls)
	}
}

func TestRealLessonSources(t *testing.T) {
	if os.Getenv("ENGLISH_REAL_SOURCE_TEST") == "" {
		t.Skip("设置 ENGLISH_REAL_SOURCE_TEST=1 才访问真实内容源")
	}
	c := newSourceClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	day := time.Now()
	news, err := c.chinaDaily(ctx, day, nil)
	if err != nil || news.SourceURL == "" || news.SourceTitle == "" || news.Summary == "" {
		t.Fatalf("China Daily 来源异常: %+v / %v", news, err)
	}
	tech, err := c.tech(ctx, day, nil)
	if err != nil || tech.SourceURL == "" || tech.SourceTitle == "" || tech.Summary == "" {
		t.Fatalf("技术来源异常: %+v / %v", tech, err)
	}
}
