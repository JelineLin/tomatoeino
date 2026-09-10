package english

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("记录不存在")

type storeDialect uint8

const (
	dialectSQLite storeDialect = iota
	dialectPostgres
)

type Store struct {
	db      *sql.DB
	dialect storeDialect
}

func OpenStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("SQLite 路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}
	// SQLite 只有一个写者。限制连接池可避免同一进程自己制造 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 SQLite 失败: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 SQLite 失败: %w", err)
	}
	return &Store{db: db, dialect: dialectSQLite}, nil
}

// OpenPostgresStore 打开 English 的生产账本。与 SQLite 不同，这里只验连接和
// schema，不在进程启动时执行 DDL；发布时必须先人工应用版本化 migration。
func OpenPostgresStore(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("ENGLISH_DATABASE_URL 不能为空")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("打开 English PostgreSQL 失败: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 English PostgreSQL 失败: %w", err)
	}
	store := &Store{db: db, dialect: dialectPostgres}
	if err := store.Ready(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Ready 是业务服务的就绪依据；SQLite 在 OpenStore 已经完成自检，PostgreSQL
// 则确认九张表都由人工 migration 建好。
func (s *Store) Ready(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("English store 未配置")
	}
	if s.dialect == dialectSQLite {
		return s.db.PingContext(ctx)
	}
	var ready bool
	err := s.db.QueryRowContext(ctx, `
		SELECT to_regclass('account.users') IS NOT NULL
		   AND to_regclass('english.learning_profiles') IS NOT NULL
		   AND to_regclass('english.lessons') IS NOT NULL
		   AND to_regclass('english.reading_attempts') IS NOT NULL
		   AND to_regclass('english.speaking_attempts') IS NOT NULL
		   AND to_regclass('english.word_results') IS NOT NULL
		   AND to_regclass('english.weak_points') IS NOT NULL
		   AND to_regclass('english.weekly_reports') IS NOT NULL
		   AND to_regclass('english.plan_versions') IS NOT NULL
		   AND to_regclass('english.job_runs') IS NOT NULL`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("检查 English PostgreSQL 结构失败: %w", err)
	}
	if !ready {
		return fmt.Errorf("English PostgreSQL migration 尚未完成")
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

var postgresTables = strings.NewReplacer(
	"learning_profiles", "english.learning_profiles",
	"reading_attempts", "english.reading_attempts",
	"speaking_attempts", "english.speaking_attempts",
	"word_results", "english.word_results",
	"weak_points", "english.weak_points",
	"weekly_reports", "english.weekly_reports",
	"plan_versions", "english.plan_versions",
	"job_runs", "english.job_runs",
	"lessons", "english.lessons",
)

// query 把 SQLite 的 ? 占位符和裸表名转换成 PostgreSQL 形式。业务 SQL 只维护
// 一份，避免两个后端在评分、幂等和任务 claim 语义上逐渐分叉。
func (s *Store) query(query string) string {
	if s.dialect != dialectPostgres {
		return query
	}
	query = postgresTables.Replace(query)
	var out strings.Builder
	out.Grow(len(query) + 16)
	parameter := 1
	for _, char := range query {
		if char == '?' {
			fmt.Fprintf(&out, "$%d", parameter)
			parameter++
		} else {
			out.WriteRune(char)
		}
	}
	return out.String()
}

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.query(query), args...)
}

func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.query(query), args...)
}

func (s *Store) queryRows(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.query(query), args...)
}

func (s *Store) txExec(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, s.query(query), args...)
}

func (s *Store) insertID(ctx context.Context, query string, args ...any) (int64, error) {
	if s.dialect == dialectPostgres {
		var id int64
		if err := s.queryRow(ctx, query+" RETURNING id", args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	result, err := s.exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) txInsertID(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	if s.dialect == dialectPostgres {
		var id int64
		if err := tx.QueryRowContext(ctx, s.query(query+" RETURNING id"), args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	result, err := s.txExec(ctx, tx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) timeArgument(value time.Time) any {
	if s.dialect == dialectPostgres {
		return value
	}
	return value.Format(time.RFC3339Nano)
}

func (s *Store) dateArgument(value string) any {
	if s.dialect == dialectPostgres {
		parsed, _ := time.Parse("2006-01-02", value)
		return parsed
	}
	return value
}

// databaseDate/databaseTime 兼容 SQLite 返回的文本与 PostgreSQL 返回的原生
// DATE/TIMESTAMPTZ，领域模型仍统一使用 string/time.Time。
type databaseDate string

func (d *databaseDate) Scan(value any) error {
	switch value := value.(type) {
	case time.Time:
		*d = databaseDate(value.Format("2006-01-02"))
	case string:
		*d = databaseDate(value)
	case []byte:
		*d = databaseDate(string(value))
	default:
		return fmt.Errorf("不支持的日期类型 %T", value)
	}
	return nil
}

type databaseTime struct{ time.Time }

func (t *databaseTime) Scan(value any) error {
	switch value := value.(type) {
	case time.Time:
		t.Time = value
		return nil
	case string:
		return t.parse(value)
	case []byte:
		return t.parse(string(value))
	default:
		return fmt.Errorf("不支持的时间类型 %T", value)
	}
}

func (t *databaseTime) parse(value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

func (s *Store) EnsureProfile(ctx context.Context, userID string) (Profile, error) {
	if strings.TrimSpace(userID) == "" {
		return Profile{}, fmt.Errorf("userID 不能为空")
	}
	now := time.Now().UTC()
	_, err := s.exec(ctx, `INSERT INTO learning_profiles
 (user_id,cet4_score,level,daily_minutes,goal,difficulty,navigation_position,content_mode,ielts_track,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id) DO NOTHING`,
		userID, 430, DefaultLevel, DefaultDailyMinutes, "提升英文阅读与口语", 1, DefaultNavigation, DefaultContentMode, DefaultIELTSTrack, s.timeArgument(now))
	if err != nil {
		return Profile{}, err
	}
	return s.Profile(ctx, userID)
}

func (s *Store) Profile(ctx context.Context, userID string) (Profile, error) {
	var p Profile
	var updated databaseTime
	err := s.queryRow(ctx, `SELECT user_id,cet4_score,level,daily_minutes,goal,difficulty,navigation_position,content_mode,ielts_track,updated_at
 FROM learning_profiles WHERE user_id=?`, userID).Scan(&p.UserID, &p.CET4Score, &p.Level, &p.DailyMinutes, &p.Goal, &p.Difficulty, &p.Navigation, &p.ContentMode, &p.IELTSTrack, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	p.UpdatedAt = updated.Time
	return p, nil
}

func (s *Store) SaveProfile(ctx context.Context, p Profile) (Profile, error) {
	if p.UserID == "" {
		return Profile{}, fmt.Errorf("userID 不能为空")
	}
	if p.CET4Score <= 0 {
		p.CET4Score = 430
	}
	if p.Level == "" {
		p.Level = DefaultLevel
	}
	switch p.Level {
	case "A2", "B1", "B2", "C1":
	default:
		return Profile{}, fmt.Errorf("level 只能是 A2/B1/B2/C1")
	}
	if p.DailyMinutes < 10 || p.DailyMinutes > 180 {
		return Profile{}, fmt.Errorf("每日学习时间需在 10～180 分钟")
	}
	p.Goal = strings.TrimSpace(p.Goal)
	if len([]rune(p.Goal)) > 500 {
		return Profile{}, fmt.Errorf("学习目标不能超过 500 字")
	}
	if p.Difficulty < 1 {
		p.Difficulty = 1
	}
	if p.Difficulty > 5 {
		p.Difficulty = 5
	}
	if p.Navigation == "" {
		if existing, err := s.Profile(ctx, p.UserID); err == nil && existing.Navigation != "" {
			p.Navigation = existing.Navigation
		} else {
			p.Navigation = DefaultNavigation
		}
	}
	switch p.Navigation {
	case "bottom", "left", "right":
	default:
		return Profile{}, fmt.Errorf("navigation_position 只能是 bottom/left/right")
	}
	existing, existingErr := s.Profile(ctx, p.UserID)
	if p.ContentMode == "" {
		if existingErr == nil && existing.ContentMode != "" {
			p.ContentMode = existing.ContentMode
		} else {
			p.ContentMode = DefaultContentMode
		}
	}
	switch p.ContentMode {
	case "balanced", "new-concept", "ielts", "china-daily", "tech":
	default:
		return Profile{}, fmt.Errorf("content_mode 只能是 balanced/new-concept/ielts/china-daily/tech")
	}
	if p.IELTSTrack == "" {
		if existingErr == nil && existing.IELTSTrack != "" {
			p.IELTSTrack = existing.IELTSTrack
		} else {
			p.IELTSTrack = DefaultIELTSTrack
		}
	}
	switch p.IELTSTrack {
	case "academic", "general":
	default:
		return Profile{}, fmt.Errorf("ielts_track 只能是 academic/general")
	}
	p.UpdatedAt = time.Now().UTC()
	_, err := s.exec(ctx, `INSERT INTO learning_profiles
 (user_id,cet4_score,level,daily_minutes,goal,difficulty,navigation_position,content_mode,ielts_track,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET cet4_score=excluded.cet4_score,level=excluded.level,
 daily_minutes=excluded.daily_minutes,goal=excluded.goal,difficulty=excluded.difficulty,
 navigation_position=excluded.navigation_position,content_mode=excluded.content_mode,
 ielts_track=excluded.ielts_track,updated_at=excluded.updated_at`,
		p.UserID, p.CET4Score, p.Level, p.DailyMinutes, p.Goal, p.Difficulty, p.Navigation, p.ContentMode, p.IELTSTrack, s.timeArgument(p.UpdatedAt))
	if err != nil {
		return Profile{}, err
	}
	return p, nil
}

func marshalArray(v any) string {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return "[]"
	}
	return string(b)
}

func (s *Store) PutLesson(ctx context.Context, l Lesson) (Lesson, bool, error) {
	if l.UserID == "" {
		return Lesson{}, false, fmt.Errorf("userID 不能为空")
	}
	if _, err := time.Parse("2006-01-02", l.Date); err != nil {
		return Lesson{}, false, fmt.Errorf("课程日期无效: %w", err)
	}
	if l.EstimatedMinutes == 0 {
		l.EstimatedMinutes = DefaultDailyMinutes
	}
	l.CreatedAt = time.Now().UTC()
	res, err := s.exec(ctx, `INSERT INTO lessons
 (user_id,lesson_date,title,passage,vocabulary_json,questions_json,difficulty,generation_reason,estimated_minutes,
 content_mode,exercise_style,syllabus_focus,source_name,source_title,source_url,source_published_at,adaptation_note,content_hash,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,lesson_date) DO NOTHING`,
		l.UserID, s.dateArgument(l.Date), l.Title, l.Passage, marshalArray(l.Vocabulary), marshalArray(l.Questions), l.Difficulty, l.GenerationReason, l.EstimatedMinutes,
		l.ContentMode, l.ExerciseStyle, l.SyllabusFocus, l.SourceName, l.SourceTitle, l.SourceURL, l.SourcePublished, l.AdaptationNote, l.ContentHash, s.timeArgument(l.CreatedAt))
	if err != nil {
		return Lesson{}, false, err
	}
	n, _ := res.RowsAffected()
	stored, err := s.LessonByDate(ctx, l.UserID, l.Date)
	return stored, n == 1, err
}

const lessonColumns = `id,user_id,lesson_date,title,passage,vocabulary_json,questions_json,difficulty,generation_reason,estimated_minutes,
content_mode,exercise_style,syllabus_focus,source_name,source_title,source_url,source_published_at,adaptation_note,content_hash,created_at`

func scanLesson(row interface{ Scan(...any) error }) (Lesson, error) {
	var l Lesson
	var vocab, questions string
	var date databaseDate
	var created databaseTime
	err := row.Scan(&l.ID, &l.UserID, &date, &l.Title, &l.Passage, &vocab, &questions, &l.Difficulty, &l.GenerationReason, &l.EstimatedMinutes,
		&l.ContentMode, &l.ExerciseStyle, &l.SyllabusFocus, &l.SourceName, &l.SourceTitle, &l.SourceURL, &l.SourcePublished, &l.AdaptationNote, &l.ContentHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Lesson{}, ErrNotFound
	}
	if err != nil {
		return Lesson{}, err
	}
	if err := json.Unmarshal([]byte(vocab), &l.Vocabulary); err != nil {
		return Lesson{}, err
	}
	if err := json.Unmarshal([]byte(questions), &l.Questions); err != nil {
		return Lesson{}, err
	}
	l.Date = string(date)
	l.CreatedAt = created.Time
	return l, nil
}

func (s *Store) LessonByDate(ctx context.Context, userID, date string) (Lesson, error) {
	return scanLesson(s.queryRow(ctx, `SELECT `+lessonColumns+` FROM lessons WHERE user_id=? AND lesson_date=?`, userID, s.dateArgument(date)))
}

func (s *Store) Lesson(ctx context.Context, userID string, id int64) (Lesson, error) {
	return scanLesson(s.queryRow(ctx, `SELECT `+lessonColumns+` FROM lessons WHERE user_id=? AND id=?`, userID, id))
}

func (s *Store) Lessons(ctx context.Context, userID string, limit int) ([]Lesson, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.queryRows(ctx, `SELECT `+lessonColumns+` FROM lessons WHERE user_id=? ORDER BY lesson_date DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lesson
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RecentSourceURLs 给课程生成器做来源去重；仅返回真实外部来源，教材型和复习型课程的空 URL 不参与。
func (s *Store) RecentSourceURLs(ctx context.Context, userID string, limit int) ([]string, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.queryRows(ctx, `SELECT source_url FROM lessons WHERE user_id=? AND source_url<>'' ORDER BY lesson_date DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) SaveReadingAttempt(ctx context.Context, userID string, lessonID int64, answers []Answer) (ReadingAttempt, error) {
	l, err := s.Lesson(ctx, userID, lessonID)
	if err != nil {
		return ReadingAttempt{}, err
	}
	provided := map[string]string{}
	for _, a := range answers {
		provided[a.QuestionID] = strings.TrimSpace(a.Value)
	}
	correct := 0
	for _, q := range l.Questions {
		if strings.EqualFold(strings.TrimSpace(q.Answer), provided[q.ID]) {
			correct++
		}
	}
	total := len(l.Questions)
	accuracy := 0.0
	if total > 0 {
		accuracy = float64(correct) / float64(total)
	}
	now := time.Now().UTC()
	id, err := s.insertID(ctx, `INSERT INTO reading_attempts
 (user_id,lesson_id,answers_json,correct_count,total_count,accuracy,completed_at) VALUES(?,?,?,?,?,?,?)`,
		userID, lessonID, marshalArray(answers), correct, total, accuracy, s.timeArgument(now))
	if err != nil {
		return ReadingAttempt{}, err
	}
	return ReadingAttempt{ID: id, UserID: userID, LessonID: lessonID, Answers: answers, Correct: correct, Total: total, Accuracy: accuracy, CompletedAt: now}, nil
}

// LatestReadingAttempt 返回用户对某一课最近一次已提交的阅读答案。
// 页面切换只销毁 UI 状态，账本中的完成记录仍可恢复；查询同时带 user_id，避免跨用户串题。
func (s *Store) LatestReadingAttempt(ctx context.Context, userID string, lessonID int64) (ReadingAttempt, error) {
	var a ReadingAttempt
	var answers string
	var completed databaseTime
	err := s.queryRow(ctx, `SELECT id,user_id,lesson_id,answers_json,correct_count,total_count,accuracy,completed_at
 FROM reading_attempts WHERE user_id=? AND lesson_id=? ORDER BY id DESC LIMIT 1`, userID, lessonID).
		Scan(&a.ID, &a.UserID, &a.LessonID, &answers, &a.Correct, &a.Total, &a.Accuracy, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return ReadingAttempt{}, ErrNotFound
	}
	if err != nil {
		return ReadingAttempt{}, err
	}
	if err := json.Unmarshal([]byte(answers), &a.Answers); err != nil {
		return ReadingAttempt{}, err
	}
	a.CompletedAt = completed.Time
	return a, nil
}

func (s *Store) SaveSpeakingAttempt(ctx context.Context, a SpeakingAttempt) (SpeakingAttempt, error) {
	if _, err := s.Lesson(ctx, a.UserID, a.LessonID); err != nil {
		return SpeakingAttempt{}, err
	}
	now := time.Now().UTC()
	a.CompletedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SpeakingAttempt{}, err
	}
	defer tx.Rollback()
	a.ID, err = s.txInsertID(ctx, tx, `INSERT INTO speaking_attempts
 (user_id,lesson_id,audio_path,duration_seconds,transcript,wpm,accuracy,score,feedback,issues_json,completed_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.UserID, a.LessonID, a.AudioPath, a.DurationSec, a.Transcript, a.WPM, a.Accuracy, a.Score, a.Feedback, marshalArray(a.Issues), s.timeArgument(now))
	if err != nil {
		return SpeakingAttempt{}, err
	}
	for _, issue := range a.Issues {
		if _, err := s.txExec(ctx, tx, `INSERT INTO word_results(user_id,speaking_attempt_id,expected_word,actual_word,issue_kind,created_at) VALUES(?,?,?,?,?,?)`,
			a.UserID, a.ID, issue.Expected, issue.Actual, issue.Kind, s.timeArgument(now)); err != nil {
			return SpeakingAttempt{}, err
		}
		if issue.Expected != "" {
			if _, err := s.txExec(ctx, tx, `INSERT INTO weak_points(user_id,point_type,point_value,occurrence_count,last_seen_at) VALUES(?,?,?,?,?)
			 ON CONFLICT(user_id,point_type,point_value) DO UPDATE SET occurrence_count=occurrence_count+1,last_seen_at=excluded.last_seen_at`,
				a.UserID, "word", strings.ToLower(issue.Expected), 1, s.timeArgument(now)); err != nil {
				return SpeakingAttempt{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return SpeakingAttempt{}, err
	}
	return a, nil
}

func (s *Store) SpeakingAttempt(ctx context.Context, userID string, id int64) (SpeakingAttempt, error) {
	var a SpeakingAttempt
	var issues string
	var completed databaseTime
	err := s.queryRow(ctx, `SELECT id,user_id,lesson_id,audio_path,duration_seconds,transcript,wpm,accuracy,score,feedback,issues_json,completed_at
 FROM speaking_attempts WHERE user_id=? AND id=?`, userID, id).Scan(&a.ID, &a.UserID, &a.LessonID, &a.AudioPath, &a.DurationSec, &a.Transcript, &a.WPM, &a.Accuracy, &a.Score, &a.Feedback, &issues, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return SpeakingAttempt{}, ErrNotFound
	}
	if err != nil {
		return SpeakingAttempt{}, err
	}
	_ = json.Unmarshal([]byte(issues), &a.Issues)
	a.CompletedAt = completed.Time
	return a, nil
}

func (s *Store) Progress(ctx context.Context, userID string, now time.Time) (ProgressSnapshot, error) {
	p, err := s.EnsureProfile(ctx, userID)
	if err != nil {
		return ProgressSnapshot{}, err
	}
	since := now.AddDate(0, 0, -28).Format("2006-01-02")
	var assigned, completed int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM lessons WHERE user_id=? AND lesson_date>=?`, userID, s.dateArgument(since)).Scan(&assigned); err != nil {
		return ProgressSnapshot{}, err
	}
	if err := s.queryRow(ctx, `SELECT COUNT(DISTINCT lesson_id) FROM (
	 SELECT lesson_id FROM reading_attempts WHERE user_id=? AND completed_at>=?
	 UNION SELECT lesson_id FROM speaking_attempts WHERE user_id=? AND completed_at>=?)`, userID, s.dateArgument(since), userID, s.dateArgument(since)).Scan(&completed); err != nil {
		return ProgressSnapshot{}, err
	}
	var readAcc, speakAcc, wpm sql.NullFloat64
	_ = s.queryRow(ctx, `SELECT AVG(accuracy) FROM reading_attempts WHERE user_id=? AND completed_at>=?`, userID, s.dateArgument(since)).Scan(&readAcc)
	_ = s.queryRow(ctx, `SELECT AVG(accuracy),AVG(wpm) FROM speaking_attempts WHERE user_id=? AND completed_at>=?`, userID, s.dateArgument(since)).Scan(&speakAcc, &wpm)
	rows, err := s.queryRows(ctx, `SELECT point_value FROM weak_points WHERE user_id=? AND point_type='word' ORDER BY occurrence_count DESC,last_seen_at DESC LIMIT 10`, userID)
	if err != nil {
		return ProgressSnapshot{}, err
	}
	defer rows.Close()
	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return ProgressSnapshot{}, err
		}
		words = append(words, w)
	}
	completion := 0.0
	if assigned > 0 {
		completion = float64(completed) / float64(assigned)
		if completion > 1 {
			completion = 1
		}
	}
	snap := ProgressSnapshot{Level: p.Level, Difficulty: p.Difficulty, CompletionRate4W: completion, ReadingAccuracy4W: readAcc.Float64, SpeakingAccuracy4W: speakAcc.Float64, SpeakingSpeedWPM: wpm.Float64, RecurringErrors: words, LessonsAssigned: assigned, LessonsCompleted: completed}
	snap.RecommendedDifficulty = RecommendDifficulty(p.Difficulty, snap)
	return snap, nil
}

func (s *Store) WeeklyReports(ctx context.Context, userID string, limit int) ([]WeeklyReport, error) {
	if limit <= 0 || limit > 52 {
		limit = 12
	}
	rows, err := s.queryRows(ctx, `SELECT id,user_id,week_start,completion_rate,reading_accuracy,speaking_wpm,problem_words_json,summary,next_focus,created_at
 FROM weekly_reports WHERE user_id=? ORDER BY week_start DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WeeklyReport
	for rows.Next() {
		var r WeeklyReport
		var words string
		var weekStart databaseDate
		var created databaseTime
		if err := rows.Scan(&r.ID, &r.UserID, &weekStart, &r.CompletionRate, &r.ReadingAccuracy, &r.SpeakingWPM, &words, &r.Summary, &r.NextFocus, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(words), &r.ProblemWords)
		r.WeekStart = string(weekStart)
		r.CreatedAt = created.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) StartJob(ctx context.Context, userID, date, kind string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.exec(ctx, `INSERT INTO job_runs(user_id,job_date,job_type,status,notification_status,created_at,updated_at)
	 VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,job_date,job_type) DO UPDATE SET status='running',error_message='',updated_at=excluded.updated_at
	 WHERE status!='succeeded'`, userID, s.dateArgument(date), kind, "running", "pending", s.timeArgument(now), s.timeArgument(now))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) FinishJob(ctx context.Context, userID, date, kind, status, notifyStatus, message string) error {
	_, err := s.exec(ctx, `UPDATE job_runs SET status=?,notification_status=CASE WHEN ?='' THEN notification_status ELSE ? END,error_message=?,updated_at=?
	 WHERE user_id=? AND job_date=? AND job_type=?`, status, notifyStatus, notifyStatus, message, s.timeArgument(time.Now().UTC()), userID, s.dateArgument(date), kind)
	return err
}

// ClaimNotification 把 pending 原子改为 sending。只有拿到 claim 的进程能调用外部微信网关，
// 即使 systemd 重试或进程在通知后崩溃，也不会在同一天重复发送。
func (s *Store) ClaimNotification(ctx context.Context, userID, date, kind string) (bool, error) {
	res, err := s.exec(ctx, `UPDATE job_runs SET notification_status='sending',updated_at=?
	 WHERE user_id=? AND job_date=? AND job_type=? AND notification_status='pending'`, s.timeArgument(time.Now().UTC()), userID, s.dateArgument(date), kind)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) SaveWeeklyReport(ctx context.Context, r WeeklyReport) (WeeklyReport, error) {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := s.exec(ctx, `INSERT INTO weekly_reports(user_id,week_start,completion_rate,reading_accuracy,speaking_wpm,problem_words_json,summary,next_focus,created_at)
 VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,week_start) DO UPDATE SET completion_rate=excluded.completion_rate,
 reading_accuracy=excluded.reading_accuracy,speaking_wpm=excluded.speaking_wpm,problem_words_json=excluded.problem_words_json,
	 summary=excluded.summary,next_focus=excluded.next_focus,created_at=excluded.created_at`, r.UserID, s.dateArgument(r.WeekStart), r.CompletionRate, r.ReadingAccuracy, r.SpeakingWPM, marshalArray(r.ProblemWords), r.Summary, r.NextFocus, s.timeArgument(r.CreatedAt))
	if err != nil {
		return WeeklyReport{}, err
	}
	reports, err := s.WeeklyReports(ctx, r.UserID, 1)
	if err != nil || len(reports) == 0 {
		return WeeklyReport{}, err
	}
	return reports[0], nil
}

func (s *Store) ApplyWeeklyDifficulty(ctx context.Context, userID, weekStart string, recommended int, reason string) error {
	p, err := s.Profile(ctx, userID)
	if err != nil || recommended == p.Difficulty {
		return err
	}
	var count int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM plan_versions WHERE user_id=? AND created_at>=?`, userID, s.dateArgument(weekStart)).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := s.txExec(ctx, tx, `UPDATE learning_profiles SET difficulty=?,updated_at=? WHERE user_id=?`, recommended, s.timeArgument(now), userID); err != nil {
		return err
	}
	if _, err := s.txExec(ctx, tx, `INSERT INTO plan_versions(user_id,old_difficulty,new_difficulty,reason,created_at) VALUES(?,?,?,?,?)`, userID, p.Difficulty, recommended, reason, s.timeArgument(now)); err != nil {
		return err
	}
	return tx.Commit()
}
