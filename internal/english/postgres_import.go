package english

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var ErrEnglishDataExists = errors.New("目标用户已有 English 数据")

type ImportResult struct {
	Profiles         int
	Lessons          int
	ReadingAttempts  int
	SpeakingAttempts int
	WordResults      int
	WeakPoints       int
	WeeklyReports    int
	PlanVersions     int
	JobRuns          int
}

func (r ImportResult) Total() int {
	return r.Profiles + r.Lessons + r.ReadingAttempts + r.SpeakingAttempts +
		r.WordResults + r.WeakPoints + r.WeeklyReports + r.PlanVersions + r.JobRuns
}

// ImportSQLiteUser 把一个旧 learner 的九张表完整归属到平台 UUID。源库全程只读，
// 目标库串行化写入；课程和口语主键重新生成并显式重映射所有子表外键。
func (s *Store) ImportSQLiteUser(ctx context.Context, source *Store, sourceUser, targetUser string) (ImportResult, error) {
	var result ImportResult
	if s == nil || s.dialect != dialectPostgres {
		return result, fmt.Errorf("目标必须是 English PostgreSQL store")
	}
	if source == nil || source.dialect != dialectSQLite {
		return result, fmt.Errorf("源必须是 English SQLite store")
	}
	sourceUser = strings.TrimSpace(sourceUser)
	targetUser = strings.TrimSpace(targetUser)
	if sourceUser == "" {
		return result, fmt.Errorf("源 user ID 不能为空")
	}
	parsedUser, err := uuid.Parse(targetUser)
	if err != nil || parsedUser.String() != strings.ToLower(targetUser) {
		return result, fmt.Errorf("目标平台 user ID 必须是规范 UUID")
	}

	sourceTx, err := source.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("锁定 English SQLite 快照失败: %w", err)
	}
	defer func() { _ = sourceTx.Rollback() }()

	targetTx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return result, fmt.Errorf("开始 English 数据迁移事务失败: %w", err)
	}
	defer func() { _ = targetTx.Rollback() }()
	if _, err := s.txExec(ctx, targetTx, `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, targetUser); err != nil {
		return result, fmt.Errorf("锁定 English 数据迁移失败: %w", err)
	}
	var active bool
	if err := targetTx.QueryRowContext(ctx,
		`SELECT status = 'active' FROM account.users WHERE id::text = $1`, targetUser).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, fmt.Errorf("目标平台用户不存在")
		}
		return result, fmt.Errorf("检查目标平台用户失败: %w", err)
	}
	if !active {
		return result, fmt.Errorf("目标平台用户不是 active 状态")
	}
	for _, table := range userScopedTables {
		var exists bool
		if err := targetTx.QueryRowContext(ctx, s.query(`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE user_id=?)`), targetUser).Scan(&exists); err != nil {
			return result, fmt.Errorf("检查目标 %s 数据失败: %w", table, err)
		}
		if exists {
			return result, ErrEnglishDataExists
		}
	}

	if err := s.importProfile(ctx, sourceTx, targetTx, sourceUser, targetUser, &result); err != nil {
		return ImportResult{}, err
	}
	lessonIDs, err := s.importLessons(ctx, sourceTx, targetTx, sourceUser, targetUser, &result)
	if err != nil {
		return ImportResult{}, err
	}
	if err := s.importReadingAttempts(ctx, sourceTx, targetTx, sourceUser, targetUser, lessonIDs, &result); err != nil {
		return ImportResult{}, err
	}
	speakingIDs, err := s.importSpeakingAttempts(ctx, sourceTx, targetTx, sourceUser, targetUser, lessonIDs, &result)
	if err != nil {
		return ImportResult{}, err
	}
	if err := s.importWordResults(ctx, sourceTx, targetTx, sourceUser, targetUser, speakingIDs, &result); err != nil {
		return ImportResult{}, err
	}
	if err := s.importRemainingUserTables(ctx, sourceTx, targetTx, sourceUser, targetUser, &result); err != nil {
		return ImportResult{}, err
	}
	if result.Total() == 0 {
		return ImportResult{}, fmt.Errorf("源用户没有可迁移的 English 数据")
	}
	if err := targetTx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("提交 English 数据迁移失败: %w", err)
	}
	return result, nil
}

func (s *Store) importProfile(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) error {
	var p Profile
	var updated databaseTime
	err := sourceTx.QueryRowContext(ctx, `SELECT user_id,cet4_score,level,daily_minutes,goal,difficulty,navigation_position,content_mode,ielts_track,updated_at
 FROM learning_profiles WHERE user_id=?`, sourceUser).Scan(&p.UserID, &p.CET4Score, &p.Level, &p.DailyMinutes, &p.Goal, &p.Difficulty, &p.Navigation, &p.ContentMode, &p.IELTSTrack, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 English 档案失败: %w", err)
	}
	_, err = s.txExec(ctx, targetTx, `INSERT INTO learning_profiles
 (user_id,cet4_score,level,daily_minutes,goal,difficulty,navigation_position,content_mode,ielts_track,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?)`, targetUser, p.CET4Score, p.Level, p.DailyMinutes, p.Goal, p.Difficulty,
		p.Navigation, p.ContentMode, p.IELTSTrack, s.timeArgument(updated.Time))
	if err != nil {
		return fmt.Errorf("迁移 English 档案失败: %w", err)
	}
	result.Profiles++
	return nil
}

func (s *Store) importLessons(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) (map[int64]int64, error) {
	rows, err := sourceTx.QueryContext(ctx, `SELECT `+lessonColumns+` FROM lessons WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return nil, fmt.Errorf("读取 English 课程失败: %w", err)
	}
	defer rows.Close()
	ids := map[int64]int64{}
	for rows.Next() {
		lesson, err := scanLesson(rows)
		if err != nil {
			return nil, fmt.Errorf("读取 English 课程失败: %w", err)
		}
		newID, err := s.txInsertID(ctx, targetTx, `INSERT INTO lessons
 (user_id,lesson_date,title,passage,vocabulary_json,questions_json,difficulty,generation_reason,estimated_minutes,
 content_mode,exercise_style,syllabus_focus,source_name,source_title,source_url,source_published_at,adaptation_note,content_hash,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, targetUser, s.dateArgument(lesson.Date), lesson.Title, lesson.Passage,
			marshalArray(lesson.Vocabulary), marshalArray(lesson.Questions), lesson.Difficulty, lesson.GenerationReason,
			lesson.EstimatedMinutes, lesson.ContentMode, lesson.ExerciseStyle, lesson.SyllabusFocus, lesson.SourceName,
			lesson.SourceTitle, lesson.SourceURL, lesson.SourcePublished, lesson.AdaptationNote, lesson.ContentHash,
			s.timeArgument(lesson.CreatedAt))
		if err != nil {
			return nil, fmt.Errorf("迁移 English 课程失败: %w", err)
		}
		ids[lesson.ID] = newID
		result.Lessons++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 English 课程失败: %w", err)
	}
	return ids, nil
}

func (s *Store) importReadingAttempts(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, lessonIDs map[int64]int64, result *ImportResult) error {
	rows, err := sourceTx.QueryContext(ctx, `SELECT lesson_id,answers_json,correct_count,total_count,accuracy,completed_at
 FROM reading_attempts WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return fmt.Errorf("读取 English 阅读记录失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var oldLessonID int64
		var answers string
		var correct, total int
		var accuracy float64
		var completed databaseTime
		if err := rows.Scan(&oldLessonID, &answers, &correct, &total, &accuracy, &completed); err != nil {
			return fmt.Errorf("读取 English 阅读记录失败: %w", err)
		}
		lessonID, ok := lessonIDs[oldLessonID]
		if !ok {
			return fmt.Errorf("阅读记录引用了未迁移课程 %d", oldLessonID)
		}
		if _, err := s.txExec(ctx, targetTx, `INSERT INTO reading_attempts
 (user_id,lesson_id,answers_json,correct_count,total_count,accuracy,completed_at) VALUES(?,?,?,?,?,?,?)`,
			targetUser, lessonID, answers, correct, total, accuracy, s.timeArgument(completed.Time)); err != nil {
			return fmt.Errorf("迁移 English 阅读记录失败: %w", err)
		}
		result.ReadingAttempts++
	}
	return rows.Err()
}

func (s *Store) importSpeakingAttempts(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, lessonIDs map[int64]int64, result *ImportResult) (map[int64]int64, error) {
	rows, err := sourceTx.QueryContext(ctx, `SELECT id,lesson_id,audio_path,duration_seconds,transcript,wpm,accuracy,score,feedback,issues_json,completed_at
 FROM speaking_attempts WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return nil, fmt.Errorf("读取 English 口语记录失败: %w", err)
	}
	defer rows.Close()
	ids := map[int64]int64{}
	for rows.Next() {
		var oldID, oldLessonID int64
		var audioPath, transcript, feedback, issues string
		var duration, wpm, accuracy, score float64
		var completed databaseTime
		if err := rows.Scan(&oldID, &oldLessonID, &audioPath, &duration, &transcript, &wpm, &accuracy, &score, &feedback, &issues, &completed); err != nil {
			return nil, fmt.Errorf("读取 English 口语记录失败: %w", err)
		}
		lessonID, ok := lessonIDs[oldLessonID]
		if !ok {
			return nil, fmt.Errorf("口语记录引用了未迁移课程 %d", oldLessonID)
		}
		newID, err := s.txInsertID(ctx, targetTx, `INSERT INTO speaking_attempts
 (user_id,lesson_id,audio_path,duration_seconds,transcript,wpm,accuracy,score,feedback,issues_json,completed_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?)`, targetUser, lessonID, audioPath, duration, transcript, wpm, accuracy, score,
			feedback, issues, s.timeArgument(completed.Time))
		if err != nil {
			return nil, fmt.Errorf("迁移 English 口语记录失败: %w", err)
		}
		ids[oldID] = newID
		result.SpeakingAttempts++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) importWordResults(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, speakingIDs map[int64]int64, result *ImportResult) error {
	rows, err := sourceTx.QueryContext(ctx, `SELECT speaking_attempt_id,expected_word,actual_word,issue_kind,created_at
 FROM word_results WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return fmt.Errorf("读取 English 单词结果失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var oldAttemptID int64
		var expected, actual, kind string
		var created databaseTime
		if err := rows.Scan(&oldAttemptID, &expected, &actual, &kind, &created); err != nil {
			return err
		}
		attemptID, ok := speakingIDs[oldAttemptID]
		if !ok {
			return fmt.Errorf("单词结果引用了未迁移口语记录 %d", oldAttemptID)
		}
		if _, err := s.txExec(ctx, targetTx, `INSERT INTO word_results
 (user_id,speaking_attempt_id,expected_word,actual_word,issue_kind,created_at) VALUES(?,?,?,?,?,?)`,
			targetUser, attemptID, expected, actual, kind, s.timeArgument(created.Time)); err != nil {
			return fmt.Errorf("迁移 English 单词结果失败: %w", err)
		}
		result.WordResults++
	}
	return rows.Err()
}

func (s *Store) importRemainingUserTables(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) error {
	if err := s.importWeakPoints(ctx, sourceTx, targetTx, sourceUser, targetUser, result); err != nil {
		return err
	}
	if err := s.importWeeklyReports(ctx, sourceTx, targetTx, sourceUser, targetUser, result); err != nil {
		return err
	}
	if err := s.importPlanVersions(ctx, sourceTx, targetTx, sourceUser, targetUser, result); err != nil {
		return err
	}
	return s.importJobRuns(ctx, sourceTx, targetTx, sourceUser, targetUser, result)
}

func (s *Store) importWeakPoints(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) error {
	rows, err := sourceTx.QueryContext(ctx, `SELECT point_type,point_value,occurrence_count,last_seen_at FROM weak_points WHERE user_id=?`, sourceUser)
	if err != nil {
		return fmt.Errorf("读取 English 弱项失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pointType, value string
		var count int
		var seen databaseTime
		if err := rows.Scan(&pointType, &value, &count, &seen); err != nil {
			return err
		}
		if _, err := s.txExec(ctx, targetTx, `INSERT INTO weak_points(user_id,point_type,point_value,occurrence_count,last_seen_at) VALUES(?,?,?,?,?)`, targetUser, pointType, value, count, s.timeArgument(seen.Time)); err != nil {
			return fmt.Errorf("迁移 English 弱项失败: %w", err)
		}
		result.WeakPoints++
	}
	return rows.Err()
}

func (s *Store) importWeeklyReports(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) error {
	rows, err := sourceTx.QueryContext(ctx, `SELECT week_start,completion_rate,reading_accuracy,speaking_wpm,problem_words_json,summary,next_focus,created_at FROM weekly_reports WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return fmt.Errorf("读取 English 周报失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var week databaseDate
		var completion, reading, wpm float64
		var words, summary, focus string
		var created databaseTime
		if err := rows.Scan(&week, &completion, &reading, &wpm, &words, &summary, &focus, &created); err != nil {
			return err
		}
		if _, err := s.txExec(ctx, targetTx, `INSERT INTO weekly_reports(user_id,week_start,completion_rate,reading_accuracy,speaking_wpm,problem_words_json,summary,next_focus,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, targetUser, s.dateArgument(string(week)), completion, reading, wpm, words, summary, focus, s.timeArgument(created.Time)); err != nil {
			return fmt.Errorf("迁移 English 周报失败: %w", err)
		}
		result.WeeklyReports++
	}
	return rows.Err()
}

func (s *Store) importPlanVersions(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) error {
	rows, err := sourceTx.QueryContext(ctx, `SELECT old_difficulty,new_difficulty,reason,created_at FROM plan_versions WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return fmt.Errorf("读取 English 学习计划失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var oldDifficulty, newDifficulty int
		var reason string
		var created databaseTime
		if err := rows.Scan(&oldDifficulty, &newDifficulty, &reason, &created); err != nil {
			return err
		}
		if _, err := s.txExec(ctx, targetTx, `INSERT INTO plan_versions(user_id,old_difficulty,new_difficulty,reason,created_at) VALUES(?,?,?,?,?)`, targetUser, oldDifficulty, newDifficulty, reason, s.timeArgument(created.Time)); err != nil {
			return fmt.Errorf("迁移 English 学习计划失败: %w", err)
		}
		result.PlanVersions++
	}
	return rows.Err()
}

func (s *Store) importJobRuns(ctx context.Context, sourceTx, targetTx *sql.Tx, sourceUser, targetUser string, result *ImportResult) error {
	rows, err := sourceTx.QueryContext(ctx, `SELECT job_date,job_type,status,notification_status,error_message,created_at,updated_at FROM job_runs WHERE user_id=? ORDER BY id`, sourceUser)
	if err != nil {
		return fmt.Errorf("读取 English 任务记录失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var date databaseDate
		var kind, status, notification, message string
		var created, updated databaseTime
		if err := rows.Scan(&date, &kind, &status, &notification, &message, &created, &updated); err != nil {
			return err
		}
		if _, err := s.txExec(ctx, targetTx, `INSERT INTO job_runs(user_id,job_date,job_type,status,notification_status,error_message,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, targetUser, s.dateArgument(string(date)), kind, status, notification, message, s.timeArgument(created.Time), s.timeArgument(updated.Time)); err != nil {
			return fmt.Errorf("迁移 English 任务记录失败: %w", err)
		}
		result.JobRuns++
	}
	return rows.Err()
}
