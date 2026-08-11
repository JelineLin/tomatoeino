// Package english 是 English Coach 的业务账本。
//
// 它和 internal/menu 共用模型连接，却不共用任何业务状态：就像支付与汇款可以共用
// 统一鉴权/网关，但订单、状态机和对账账本必须彼此隔离。
package english

import "time"

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
	Word      string `json:"word"`
	Meaning   string `json:"meaning"`
	Example   string `json:"example"`
	Pronounce string `json:"pronunciation,omitempty"`
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
	l.Questions = append([]Question(nil), l.Questions...)
	for i := range l.Questions {
		l.Questions[i].Answer = ""
		l.Questions[i].Explain = ""
	}
	return l
}

type Answer struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
}

type ReadingAttempt struct {
	ID          int64     `json:"id"`
	UserID      string    `json:"user_id"`
	LessonID    int64     `json:"lesson_id"`
	Answers     []Answer  `json:"answers"`
	Correct     int       `json:"correct"`
	Total       int       `json:"total"`
	Accuracy    float64   `json:"accuracy"`
	CompletedAt time.Time `json:"completed_at"`
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
