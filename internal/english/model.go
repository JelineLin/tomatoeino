// Package english 是 English Coach 的业务账本。
//
// 它和 internal/menu 共用模型连接，却不共用任何业务状态：就像支付与汇款可以共用
// 统一鉴权/网关，但订单、状态机和对账账本必须彼此隔离。
package english

import (
	"strings"
	"time"
)

const (
	DefaultLevel        = "B1"
	DefaultDailyMinutes = 45
	DefaultEnglishModel = "doubao-seed-2.0-pro"
	DefaultNavigation   = "bottom"
	DefaultContentMode  = "balanced"
	DefaultIELTSTrack   = "academic"
)

type Profile struct {
	UserID       string    `json:"user_id"`
	CET4Score    int       `json:"cet4_score"`
	Level        string    `json:"level"`
	DailyMinutes int       `json:"daily_minutes"`
	Goal         string    `json:"goal"`
	Difficulty   int       `json:"difficulty"`
	Navigation   string    `json:"navigation_position"`
	ContentMode  string    `json:"content_mode"`
	IELTSTrack   string    `json:"ielts_track"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Vocabulary struct {
	Phrase        string `json:"phrase,omitempty"`
	PhraseMeaning string `json:"phrase_meaning,omitempty"`
	Word          string `json:"word"`
	Meaning       string `json:"meaning"`
	Example       string `json:"example"`
	Pronounce     string `json:"pronunciation,omitempty"`
}

type Question struct {
	ID      string   `json:"id"`
	Type    string   `json:"type,omitempty"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
	Answer  string   `json:"answer,omitempty"`
	Explain string   `json:"explanation,omitempty"`
}

type Lesson struct {
	ID               int64        `json:"id"`
	UserID           string       `json:"user_id"`
	Date             string       `json:"date"`
	Title            string       `json:"title"`
	Passage          string       `json:"passage"`
	Vocabulary       []Vocabulary `json:"vocabulary"`
	Questions        []Question   `json:"questions"`
	Difficulty       int          `json:"difficulty"`
	GenerationReason string       `json:"generation_reason"`
	EstimatedMinutes int          `json:"estimated_minutes"`
	ContentMode      string       `json:"content_mode"`
	ExerciseStyle    string       `json:"exercise_style"`
	SyllabusFocus    string       `json:"syllabus_focus"`
	SourceName       string       `json:"source_name"`
	SourceTitle      string       `json:"source_title"`
	SourceURL        string       `json:"source_url"`
	SourcePublished  string       `json:"source_published_at"`
	AdaptationNote   string       `json:"adaptation_note"`
	ContentHash      string       `json:"-"`
	CreatedAt        time.Time    `json:"created_at"`
}

// PublicLesson 隐去题目答案和解析。正确答案只留在服务端账本中参与评分，不能像把
// 支付签名密钥塞进响应一样，随题目一起下发给浏览器。
func PublicLesson(l Lesson) Lesson {
	l.Vocabulary = append([]Vocabulary(nil), l.Vocabulary...)
	for i := range l.Vocabulary {
		if strings.TrimSpace(l.Vocabulary[i].Phrase) == "" {
			l.Vocabulary[i].Phrase = contextualPhrase(l.Vocabulary[i].Word, l.Vocabulary[i].Example, l.Passage)
		}
	}
	l.Questions = append([]Question(nil), l.Questions...)
	for i := range l.Questions {
		l.Questions[i].Answer = ""
		l.Questions[i].Explain = ""
	}
	return l
}

// contextualPhrase 为旧课程补一个包含目标词的短语片段。新课程由生成器直接给出
// 高质量搭配；这里只做向后兼容，不重写已经落账的历史课程。
func contextualPhrase(word string, contexts ...string) string {
	word = strings.TrimSpace(word)
	for _, context := range contexts {
		fields := strings.Fields(context)
		for i, field := range fields {
			clean := strings.Trim(field, `.,;:!?"'()[]{} `)
			if !legacyWordForm(clean, word) {
				continue
			}
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 3
			if end > len(fields) {
				end = len(fields)
			}
			return strings.Join(fields[start:end], " ")
		}
	}
	return word
}

func legacyWordForm(candidate, word string) bool {
	candidate = strings.ToLower(candidate)
	word = strings.ToLower(word)
	if candidate == word {
		return true
	}
	if strings.HasPrefix(candidate, word) {
		switch candidate[len(word):] {
		case "s", "es", "ed", "ing", "er", "ers":
			return true
		}
	}
	if strings.HasSuffix(word, "y") && len(word) > 1 && candidate == strings.TrimSuffix(word, "y")+"ies" {
		return true
	}
	return false
}

type Answer struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
}

// ReadingReview 只随已提交的 ReadingAttempt 返回。课程下发时仍通过 PublicLesson
// 隐去答案，避免用户在作答前从网络响应中直接看到评分依据。
type ReadingReview struct {
	QuestionID    string `json:"question_id"`
	CorrectAnswer string `json:"correct_answer"`
	IsCorrect     bool   `json:"is_correct"`
	Explanation   string `json:"explanation"`
}

type ReadingAttempt struct {
	ID          int64           `json:"id"`
	UserID      string          `json:"user_id"`
	LessonID    int64           `json:"lesson_id"`
	Answers     []Answer        `json:"answers"`
	Review      []ReadingReview `json:"review,omitempty"`
	Correct     int             `json:"correct"`
	Total       int             `json:"total"`
	Accuracy    float64         `json:"accuracy"`
	CompletedAt time.Time       `json:"completed_at"`
}

// WithReadingReview 在确认用户已经提交后，把服务端保存的答案和解析装配到答题记录。
// 它不修改课程，也不会影响 PublicLesson 的脱敏结果。
func WithReadingReview(attempt ReadingAttempt, lesson Lesson) ReadingAttempt {
	provided := make(map[string]string, len(attempt.Answers))
	for _, answer := range attempt.Answers {
		provided[answer.QuestionID] = answer.Value
	}
	attempt.Review = make([]ReadingReview, 0, len(lesson.Questions))
	for _, question := range lesson.Questions {
		attempt.Review = append(attempt.Review, ReadingReview{
			QuestionID:    question.ID,
			CorrectAnswer: question.Answer,
			IsCorrect:     strings.EqualFold(strings.TrimSpace(question.Answer), strings.TrimSpace(provided[question.ID])),
			Explanation:   question.Explain,
		})
	}
	return attempt
}

type WordIssue struct {
	Expected string `json:"expected"`
	Actual   string `json:"actual,omitempty"`
	Kind     string `json:"kind"` // missing / misread / repeated
}

type SpeakingAttempt struct {
	ID          int64       `json:"id"`
	UserID      string      `json:"user_id"`
	LessonID    int64       `json:"lesson_id"`
	AudioPath   string      `json:"-"`
	DurationSec float64     `json:"duration_seconds"`
	Transcript  string      `json:"transcript"`
	WPM         float64     `json:"wpm"`
	Accuracy    float64     `json:"accuracy"`
	Score       float64     `json:"score"`
	Feedback    string      `json:"feedback"`
	Issues      []WordIssue `json:"issues"`
	CompletedAt time.Time   `json:"completed_at"`
}

type ProgressSnapshot struct {
	Level                 string   `json:"level"`
	Difficulty            int      `json:"difficulty"`
	CompletionRate4W      float64  `json:"completion_rate_4w"`
	ReadingAccuracy4W     float64  `json:"reading_accuracy_4w"`
	SpeakingAccuracy4W    float64  `json:"speaking_accuracy_4w"`
	SpeakingSpeedWPM      float64  `json:"speaking_speed_wpm"`
	RecurringErrors       []string `json:"recurring_errors"`
	WeakPatterns          []string `json:"weak_patterns"`
	LessonsAssigned       int      `json:"lessons_assigned"`
	LessonsCompleted      int      `json:"lessons_completed"`
	RecommendedDifficulty int      `json:"recommended_difficulty"`
}

type WeeklyReport struct {
	ID              int64     `json:"id"`
	UserID          string    `json:"user_id"`
	WeekStart       string    `json:"week_start"`
	CompletionRate  float64   `json:"completion_rate"`
	ReadingAccuracy float64   `json:"reading_accuracy"`
	SpeakingWPM     float64   `json:"speaking_wpm"`
	ProblemWords    []string  `json:"problem_words"`
	Summary         string    `json:"summary"`
	NextFocus       string    `json:"next_focus"`
	CreatedAt       time.Time `json:"created_at"`
}

type Transcript struct {
	Text  string      `json:"text"`
	Words []TimedWord `json:"words,omitempty"`
}

type TimedWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}
