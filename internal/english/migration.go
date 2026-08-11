package english

import (
	"database/sql"
	"fmt"
)

// migrate 在进程启动时做幂等建表。生产部署不依赖外部 migration CLI，避免出现
// “二进制已升级、账本结构还没升级”的半完成状态。
func migrate(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS learning_profiles (
  user_id TEXT PRIMARY KEY,
  cet4_score INTEGER NOT NULL DEFAULT 430,
  level TEXT NOT NULL DEFAULT 'B1',
  daily_minutes INTEGER NOT NULL DEFAULT 45,
  goal TEXT NOT NULL DEFAULT '',
  difficulty INTEGER NOT NULL DEFAULT 1,
  navigation_position TEXT NOT NULL DEFAULT 'bottom',
  content_mode TEXT NOT NULL DEFAULT 'balanced',
  ielts_track TEXT NOT NULL DEFAULT 'academic',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS lessons (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  lesson_date TEXT NOT NULL,
  title TEXT NOT NULL,
  passage TEXT NOT NULL,
  vocabulary_json TEXT NOT NULL,
  questions_json TEXT NOT NULL,
  difficulty INTEGER NOT NULL,
  generation_reason TEXT NOT NULL,
  estimated_minutes INTEGER NOT NULL,
  content_mode TEXT NOT NULL DEFAULT '',
  exercise_style TEXT NOT NULL DEFAULT '',
  syllabus_focus TEXT NOT NULL DEFAULT '',
  source_name TEXT NOT NULL DEFAULT '',
  source_title TEXT NOT NULL DEFAULT '',
  source_url TEXT NOT NULL DEFAULT '',
  source_published_at TEXT NOT NULL DEFAULT '',
  adaptation_note TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(user_id, lesson_date)
);
CREATE INDEX IF NOT EXISTS idx_lessons_user_date ON lessons(user_id, lesson_date DESC);
CREATE TABLE IF NOT EXISTS reading_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  lesson_id INTEGER NOT NULL,
  answers_json TEXT NOT NULL,
  correct_count INTEGER NOT NULL,
  total_count INTEGER NOT NULL,
  accuracy REAL NOT NULL,
  completed_at TEXT NOT NULL,
  FOREIGN KEY(lesson_id) REFERENCES lessons(id)
);
CREATE INDEX IF NOT EXISTS idx_reading_user_time ON reading_attempts(user_id, completed_at DESC);
CREATE TABLE IF NOT EXISTS speaking_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  lesson_id INTEGER NOT NULL,
  audio_path TEXT NOT NULL,
  duration_seconds REAL NOT NULL,
  transcript TEXT NOT NULL,
  wpm REAL NOT NULL,
  accuracy REAL NOT NULL,
  score REAL NOT NULL,
  feedback TEXT NOT NULL,
  issues_json TEXT NOT NULL,
  completed_at TEXT NOT NULL,
  FOREIGN KEY(lesson_id) REFERENCES lessons(id)
);
CREATE INDEX IF NOT EXISTS idx_speaking_user_time ON speaking_attempts(user_id, completed_at DESC);
CREATE TABLE IF NOT EXISTS word_results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  speaking_attempt_id INTEGER NOT NULL,
  expected_word TEXT NOT NULL,
  actual_word TEXT NOT NULL,
  issue_kind TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(speaking_attempt_id) REFERENCES speaking_attempts(id)
);
CREATE INDEX IF NOT EXISTS idx_word_results_user_word ON word_results(user_id, expected_word);
CREATE TABLE IF NOT EXISTS weak_points (
  user_id TEXT NOT NULL,
  point_type TEXT NOT NULL,
  point_value TEXT NOT NULL,
  occurrence_count INTEGER NOT NULL DEFAULT 1,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY(user_id, point_type, point_value)
);
CREATE TABLE IF NOT EXISTS weekly_reports (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  week_start TEXT NOT NULL,
  completion_rate REAL NOT NULL,
  reading_accuracy REAL NOT NULL,
  speaking_wpm REAL NOT NULL,
  problem_words_json TEXT NOT NULL,
  summary TEXT NOT NULL,
  next_focus TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(user_id, week_start)
);
CREATE TABLE IF NOT EXISTS plan_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  old_difficulty INTEGER NOT NULL,
  new_difficulty INTEGER NOT NULL,
  reason TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS job_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  job_date TEXT NOT NULL,
  job_type TEXT NOT NULL,
  status TEXT NOT NULL,
  notification_status TEXT NOT NULL DEFAULT 'pending',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, job_date, job_type)
);`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	columns := []struct{ table, name, definition string }{
		{"learning_profiles", "navigation_position", "TEXT NOT NULL DEFAULT 'bottom'"},
		{"learning_profiles", "content_mode", "TEXT NOT NULL DEFAULT 'balanced'"},
		{"learning_profiles", "ielts_track", "TEXT NOT NULL DEFAULT 'academic'"},
		{"lessons", "content_mode", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "exercise_style", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "syllabus_focus", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "source_name", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "source_title", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "source_url", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "source_published_at", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "adaptation_note", "TEXT NOT NULL DEFAULT ''"},
		{"lessons", "content_hash", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		if err := ensureColumn(db, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

// ensureColumn 兼容已上线的 SQLite。SQLite 的 ADD COLUMN 没有通用的 IF NOT EXISTS，
// 因此先读表结构再升级，像账务表加字段一样保证重复启动不会重复执行 DDL。
func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var id, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&id, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	// table/column/definition 都是代码内常量，不接收外部输入。
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}
