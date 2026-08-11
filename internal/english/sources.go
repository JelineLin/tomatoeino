package english

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	ModeBalanced     = "balanced"
	ModeNewConcept   = "new-concept"
	ModeIELTS        = "ielts"
	ModeChinaDaily   = "china-daily"
	ModeTech         = "tech"
	ModeWeeklyReview = "weekly-review"
)

// LessonSource 是生成模型的“事实底稿”，而不是直接展示的课文。新闻课程只把标题、
// 摘要和链接交给模型做分级原创改写，避免把第三方文章整篇搬进自己的数据库。
type LessonSource struct {
	ContentMode     string
	ExerciseStyle   string
	SyllabusFocus   string
	SourceName      string
	SourceTitle     string
	SourceURL       string
	SourcePublished string
	Summary         string
	AdaptationNote  string
	ContentHash     string
}

type sourceClient struct{ http *http.Client }

func newSourceClient() *sourceClient {
	return &sourceClient{http: &http.Client{Timeout: 12 * time.Second}}
}

func contentModeForDay(profile Profile, day time.Time) string {
	if profile.ContentMode != "" && profile.ContentMode != ModeBalanced {
		return profile.ContentMode
	}
	switch day.Weekday() {
	case time.Monday:
		return ModeNewConcept
	case time.Tuesday:
		return ModeChinaDaily
	case time.Wednesday:
		return ModeTech
	case time.Thursday:
		return ModeIELTS
	default:
		return ModeWeeklyReview
	}
}

func (c *sourceClient) resolve(ctx context.Context, profile Profile, day time.Time, excludedURLs []string) LessonSource {
	mode := contentModeForDay(profile, day)
	var source LessonSource
	switch mode {
	case ModeNewConcept:
		source = newConceptSource(profile)
	case ModeIELTS:
		source = ieltsSource(profile)
	case ModeChinaDaily:
		var err error
		source, err = c.chinaDaily(ctx, day, excludedURLs)
		if err != nil {
			source = fallbackSource(mode, "中国时事与社会生活", "China Daily 暂时不可读取，本课按同类主题原创生成")
		}
	case ModeTech:
		var err error
		source, err = c.tech(ctx, day, excludedURLs)
		if err != nil {
			source = fallbackSource(mode, "软件工程与互联网技术", "IT 官方内容源暂时不可读取，本课按同类主题原创生成")
		}
	default:
		source = weeklyReviewSource(profile)
	}
	if source.ContentMode == "" {
		source.ContentMode = mode
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{source.ContentMode, source.SourceURL, source.SourceTitle, day.Format("2006-01-02")}, "|")))
	source.ContentHash = hex.EncodeToString(sum[:])
	return source
}

func newConceptSource(profile Profile) LessonSource {
	focus := "新概念英语第二册风格：时态、从句与日常叙事"
	if profile.Difficulty >= 3 || profile.Level == "B2" || profile.Level == "C1" {
		focus = "新概念英语第二册到第三册衔接：段落组织、复杂句与观点表达"
	}
	return LessonSource{
		ContentMode:    ModeNewConcept,
		ExerciseStyle:  "教材能力训练",
		SyllabusFocus:  focus,
		SourceName:     "新概念英语能力路径",
		SourceTitle:    focus,
		AdaptationNote: "依据新概念英语的能力进阶与练习风格原创，不复制教材课文",
	}
}

func ieltsSource(profile Profile) LessonSource {
	track := profile.IELTSTrack
	if track == "" {
		track = DefaultIELTSTrack
	}
	label := "Academic"
	if track == "general" {
		label = "General Training"
	}
	return LessonSource{
		ContentMode:    ModeIELTS,
		ExerciseStyle:  "IELTS " + label + " 风格阅读",
		SyllabusFocus:  "主旨、细节定位、作者观点与信息匹配",
		SourceName:     "IELTS 官方题型参考",
		SourceTitle:    "IELTS " + label + " sample test format",
		SourceURL:      "https://ielts.org/take-a-test/preparation-resources/sample-test-questions",
		AdaptationNote: "参照 IELTS 官方公开题型生成原创练习，不是官方真题",
	}
}

func weeklyReviewSource(profile Profile) LessonSource {
	return LessonSource{
		ContentMode:    ModeWeeklyReview,
		ExerciseStyle:  "每周综合复习",
		SyllabusFocus:  "复习近期问题词、阅读薄弱点与口语表达",
		SourceName:     "English Coach 学习记录",
		SourceTitle:    fmt.Sprintf("%s 难度 %d/5 每周复习", profile.Level, profile.Difficulty),
		AdaptationNote: "根据个人最近四周表现原创生成",
	}
}

func fallbackSource(mode, topic, note string) LessonSource {
	return LessonSource{ContentMode: mode, ExerciseStyle: "分级原创阅读", SyllabusFocus: topic, SourceName: "English Coach 原创", SourceTitle: topic, AdaptationNote: note}
}

var chinaDailyLink = regexp.MustCompile(`(?i)(?:https?:)?//www\.chinadaily\.com\.cn/a/\d{6}/\d{2}/[^"'<> ]+\.html`)

func (c *sourceClient) chinaDaily(ctx context.Context, day time.Time, excluded []string) (LessonSource, error) {
	body, err := c.get(ctx, "https://www.chinadaily.com.cn/china", 3<<20)
	if err != nil {
		return LessonSource{}, err
	}
	seen := make(map[string]bool)
	var candidates []string
	for _, raw := range chinaDailyLink.FindAllString(string(body), -1) {
		link := raw
		if strings.HasPrefix(link, "//") {
			link = "https:" + link
		}
		link = strings.Replace(link, "http://", "https://", 1)
		if !seen[link] {
			seen[link] = true
			candidates = append(candidates, link)
		}
	}
	if len(candidates) == 0 {
		return LessonSource{}, fmt.Errorf("China Daily 首页没有文章链接")
	}
	excludedSet := stringSet(excluded)
	start := day.YearDay() % len(candidates)
	for offset := 0; offset < len(candidates) && offset < 20; offset++ {
		link := candidates[(start+offset)%len(candidates)]
		if excludedSet[link] {
			continue
		}
		title, description, err := c.articleMetadata(ctx, link)
		if err != nil || title == "" {
			continue
		}
		published := ""
		if match := regexp.MustCompile(`/a/(\d{6})/(\d{2})/`).FindStringSubmatch(link); len(match) == 3 {
			published = match[1][:4] + "-" + match[1][4:] + "-" + match[2]
		}
		return LessonSource{
			ContentMode:     ModeChinaDaily,
			ExerciseStyle:   "新闻英语：主旨与事实细节",
			SyllabusFocus:   "时事词汇、新闻结构、事实与观点区分",
			SourceName:      "China Daily",
			SourceTitle:     title,
			SourceURL:       link,
			SourcePublished: published,
			Summary:         truncate(description, 1200),
			AdaptationNote:  "根据 China Daily 标题与摘要重新编写为分级英语，不复制原文",
		}, nil
	}
	return LessonSource{}, fmt.Errorf("China Daily 没有未使用的新文章")
}

type feedDefinition struct{ name, url string }

var techFeeds = []feedDefinition{
	{"GitHub Engineering", "https://github.blog/engineering/feed/"},
	{"Google Search Central", "https://feeds.feedburner.com/blogspot/amDG"},
	{"Cloudflare Blog", "https://blog.cloudflare.com/rss/"},
}

type rssDocument struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			Published   string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (c *sourceClient) tech(ctx context.Context, day time.Time, excluded []string) (LessonSource, error) {
	excludedSet := stringSet(excluded)
	start := day.YearDay() % len(techFeeds)
	var lastErr error
	for feedOffset := range techFeeds {
		feed := techFeeds[(start+feedOffset)%len(techFeeds)]
		body, err := c.get(ctx, feed.url, 5<<20)
		if err != nil {
			lastErr = err
			continue
		}
		var doc rssDocument
		if err := xml.Unmarshal(body, &doc); err != nil {
			lastErr = err
			continue
		}
		if len(doc.Channel.Items) == 0 {
			lastErr = fmt.Errorf("%s RSS 为空", feed.name)
			continue
		}
		itemStart := day.YearDay() % len(doc.Channel.Items)
		for itemOffset := range doc.Channel.Items {
			item := doc.Channel.Items[(itemStart+itemOffset)%len(doc.Channel.Items)]
			link := strings.TrimSpace(item.Link)
			if !safeHTTPURL(link) || excludedSet[link] {
				continue
			}
			return LessonSource{
				ContentMode:     ModeTech,
				ExerciseStyle:   "技术英语：概念解释与因果关系",
				SyllabusFocus:   "软件工程词汇、系统设计表达、因果与权衡",
				SourceName:      feed.name,
				SourceTitle:     cleanText(item.Title),
				SourceURL:       link,
				SourcePublished: normalizeDate(item.Published),
				Summary:         truncate(cleanText(item.Description), 1200),
				AdaptationNote:  "根据官方技术博客标题与摘要重新编写为分级英语，不复制原文",
			}, nil
		}
	}
	return LessonSource{}, lastErr
}

func (c *sourceClient) get(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "EnglishCoach/1.0 (+https://jelinelin.com)")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s 返回 %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

func (c *sourceClient) articleMetadata(ctx context.Context, rawURL string) (string, string, error) {
	if parsed, err := url.Parse(rawURL); err != nil || parsed.Hostname() != "www.chinadaily.com.cn" {
		return "", "", fmt.Errorf("不允许的文章来源")
	}
	body, err := c.get(ctx, rawURL, 2<<20)
	if err != nil {
		return "", "", err
	}
	page := string(body)
	title := firstMatch(page, `(?is)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']+)["']`, `(?is)<title[^>]*>(.*?)</title>`)
	description := firstMatch(page, `(?is)<meta[^>]+name=["']description["'][^>]+content=["']([^"']+)["']`, `(?is)<meta[^>]+property=["']og:description["'][^>]+content=["']([^"']+)["']`)
	title = strings.TrimSpace(strings.TrimSuffix(cleanText(title), " - Chinadaily.com.cn"))
	return title, cleanText(description), nil
}

func firstMatch(value string, patterns ...string) string {
	for _, pattern := range patterns {
		if match := regexp.MustCompile(pattern).FindStringSubmatch(value); len(match) == 2 {
			return match[1]
		}
	}
	return ""
}

var htmlTag = regexp.MustCompile(`(?s)<[^>]*>`)
var whitespace = regexp.MustCompile(`\s+`)

func cleanText(value string) string {
	value = html.UnescapeString(value)
	value = htmlTag.ReplaceAllString(value, " ")
	return strings.TrimSpace(whitespace.ReplaceAllString(value, " "))
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 02 Jan 2006 15:04:05 -0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return value
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func safeHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Hostname() != ""
}
