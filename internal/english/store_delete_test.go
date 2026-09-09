package english

import (
	"context"
	"testing"
)

// 删号只该删这一个人的数据——同库里的其他学习者必须原封不动。
func TestDeleteUserRemovesOnlyThatLearner(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	for _, userID := range []string{"alice", "bob"} {
		lesson, _, err := store.PutLesson(ctx, Lesson{
			UserID: userID, Date: "2026-09-09", Title: "T", Passage: "Text",
			Questions: []Question{{ID: "q1", Answer: "A", Explain: "because"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SaveReadingAttempt(ctx, userID, lesson.ID, []Answer{{QuestionID: "q1", Value: "A"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SaveSpeakingAttempt(ctx, SpeakingAttempt{
			UserID: userID, LessonID: lesson.ID, AudioPath: "data/english/audio/" + userID + "/a.ogg",
			DurationSec: 3, Transcript: "hello",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.EnsureProfile(ctx, userID); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := store.AudioPathsFor(ctx, "alice")
	if err != nil || len(paths) != 1 {
		t.Fatalf("AudioPathsFor() = %v, %v", paths, err)
	}

	if err := store.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, table := range userScopedTables {
		var remaining int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE user_id = ?", "alice").Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Errorf("%s 仍留有 alice 的 %d 条数据", table, remaining)
		}
		var others int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE user_id = ?", "bob").Scan(&others); err != nil {
			t.Fatal(err)
		}
		if table == "lessons" && others == 0 {
			t.Errorf("%s 里 bob 的数据被误删", table)
		}
	}

	// 幂等：删除任务会重试，第二次必须安然无恙。
	if err := store.DeleteUser(ctx, "alice"); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
	if err := store.DeleteUser(ctx, "never-existed"); err != nil {
		t.Fatalf("删除从未存在的用户应幂等: %v", err)
	}
	if err := store.DeleteUser(ctx, "  "); err == nil {
		t.Error("空 userID 必须报错，绝不能变成清库")
	}
}

// 新加一张带 user_id 的表却忘了加进 userScopedTables，账号删除就会留下残渣。
// 这个测试直接问库要真相，不依赖任何人记得改列表。
func TestDeleteUserCoversEverySchemaTable(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	rows, err := store.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	covered := make(map[string]bool, len(userScopedTables))
	for _, table := range userScopedTables {
		covered[table] = true
	}
	for _, table := range tables {
		columns, err := store.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			t.Fatal(err)
		}
		hasUserID := false
		for columns.Next() {
			var (
				cid, notNull, primaryKey int
				name, columnType         string
				defaultValue             any
			)
			if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				columns.Close()
				t.Fatal(err)
			}
			if name == "user_id" {
				hasUserID = true
			}
		}
		columns.Close()
		if hasUserID && !covered[table] {
			t.Errorf("表 %s 带 user_id 却不在 userScopedTables 里——账号删除会漏掉它", table)
		}
		if !hasUserID && covered[table] {
			t.Errorf("表 %s 没有 user_id，不该出现在 userScopedTables 里", table)
		}
	}
}
