package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const menuWriteTimeout = 5 * time.Second

// ErrMenuDataExists prevents the one-time JSON importer from overwriting an
// account that has already started writing PostgreSQL data.
var ErrMenuDataExists = errors.New("Menu PostgreSQL 已存在该用户的数据")

// PostgresRepository is the durable storage boundary for Menu business data.
// The agent keeps small per-user snapshots in memory, while every accepted write
// is committed to PostgreSQL before it is reported as successful.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Ready verifies that the manually applied Menu migration is present.
func (r *PostgresRepository) Ready(ctx context.Context) error {
	var ready bool
	err := r.pool.QueryRow(ctx, `
		SELECT to_regclass('menu.meals') IS NOT NULL
		   AND to_regclass('menu.inventory_items') IS NOT NULL
		   AND to_regclass('menu.profiles') IS NOT NULL`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("检查 Menu 数据库结构失败: %w", err)
	}
	if !ready {
		return fmt.Errorf("Menu PostgreSQL migration 尚未完成")
	}
	return nil
}

// NewPostgresStores loads one platform user's state and wires all later writes
// to PostgreSQL. userID must be the canonical UUID issued by account-server.
func NewPostgresStores(ctx context.Context, repo *PostgresRepository, userID string) (*HistoryStore, *InventoryStore, *ProfileStore, error) {
	if repo == nil || repo.pool == nil {
		return nil, nil, nil, fmt.Errorf("Menu PostgreSQL repository 未配置")
	}
	if !canonicalMenuUserID(userID) {
		return nil, nil, nil, fmt.Errorf("Menu PostgreSQL user ID 必须是规范 UUID")
	}
	days, err := repo.loadHistory(ctx, userID)
	if err != nil {
		return nil, nil, nil, err
	}
	items, err := repo.loadInventory(ctx, userID)
	if err != nil {
		return nil, nil, nil, err
	}
	profile, err := repo.loadProfile(ctx, userID)
	if err != nil {
		return nil, nil, nil, err
	}

	history := &HistoryStore{days: days}
	history.persist = func(next []Day) error {
		writeCtx, cancel := context.WithTimeout(context.Background(), menuWriteTimeout)
		defer cancel()
		return repo.saveHistory(writeCtx, userID, next)
	}
	inventory := &InventoryStore{items: items, now: time.Now}
	inventory.persist = func(next []InventoryItem) error {
		writeCtx, cancel := context.WithTimeout(context.Background(), menuWriteTimeout)
		defer cancel()
		return repo.saveInventory(writeCtx, userID, next)
	}
	profiles := &ProfileStore{p: profile}
	profiles.persist = func(next Profile) error {
		writeCtx, cancel := context.WithTimeout(context.Background(), menuWriteTimeout)
		defer cancel()
		return repo.saveProfile(writeCtx, userID, next)
	}
	return history, inventory, profiles, nil
}

func (r *PostgresRepository) loadHistory(ctx context.Context, userID string) ([]Day, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT meal_date::text, meal_type, meal_time, dishes, feedback
		FROM menu.meals
		WHERE user_id = $1
		ORDER BY meal_date, CASE meal_type WHEN 'lunch' THEN 1 WHEN 'fruit' THEN 2 ELSE 3 END`, userID)
	if err != nil {
		return nil, fmt.Errorf("读取 Menu 历史失败: %w", err)
	}
	defer rows.Close()

	byDate := make(map[string]int)
	days := make([]Day, 0)
	for rows.Next() {
		var date, mealType, mealTime string
		var dishesRaw, feedbackRaw []byte
		if err := rows.Scan(&date, &mealType, &mealTime, &dishesRaw, &feedbackRaw); err != nil {
			return nil, fmt.Errorf("解析 Menu 历史行失败: %w", err)
		}
		var dishes []Dish
		if err := json.Unmarshal(dishesRaw, &dishes); err != nil {
			return nil, fmt.Errorf("解析 %s %s 菜品失败: %w", date, mealType, err)
		}
		meal := &Meal{Time: mealTime, Dishes: dishes}
		if len(feedbackRaw) > 0 {
			if err := json.Unmarshal(feedbackRaw, &meal.Feedback); err != nil {
				return nil, fmt.Errorf("解析 %s %s 反馈失败: %w", date, mealType, err)
			}
		}
		idx, ok := byDate[date]
		if !ok {
			idx = len(days)
			byDate[date] = idx
			days = append(days, Day{Date: date})
		}
		if !validMealField(mealType) {
			return nil, fmt.Errorf("数据库包含无效餐别 %q", mealType)
		}
		days[idx].setMeal(mealType, meal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 Menu 历史失败: %w", err)
	}
	return days, nil
}

func (r *PostgresRepository) saveHistory(ctx context.Context, userID string, days []Day) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开始 Menu 历史事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM menu.meals WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("清理旧 Menu 历史失败: %w", err)
	}
	for _, day := range days {
		if _, err := time.Parse("2006-01-02", day.Date); err != nil {
			return fmt.Errorf("Menu 历史日期 %q 无效", day.Date)
		}
		for _, mealKind := range mealOrder {
			meal := day.mealOf(mealKind.field)
			if meal == nil {
				continue
			}
			dishList := meal.Dishes
			if dishList == nil {
				dishList = []Dish{}
			}
			dishes, err := json.Marshal(dishList)
			if err != nil {
				return fmt.Errorf("序列化 Menu 菜品失败: %w", err)
			}
			var feedback []byte
			if meal.Feedback != nil {
				feedback, err = json.Marshal(meal.Feedback)
				if err != nil {
					return fmt.Errorf("序列化 Menu 反馈失败: %w", err)
				}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO menu.meals(user_id, meal_date, meal_type, meal_time, dishes, feedback)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				userID, day.Date, mealKind.field, meal.Time, dishes, feedback); err != nil {
				return fmt.Errorf("写入 Menu 历史失败: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交 Menu 历史事务失败: %w", err)
	}
	return nil
}

func (r *PostgresRepository) loadInventory(ctx context.Context, userID string) ([]InventoryItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT name, quantity, unit, stocked_at
		FROM menu.inventory_items WHERE user_id = $1 ORDER BY position, name`, userID)
	if err != nil {
		return nil, fmt.Errorf("读取 Menu 库存失败: %w", err)
	}
	defer rows.Close()
	items := make([]InventoryItem, 0)
	for rows.Next() {
		var item InventoryItem
		var stocked pgtype.Timestamptz
		if err := rows.Scan(&item.Name, &item.Quantity, &item.Unit, &stocked); err != nil {
			return nil, fmt.Errorf("解析 Menu 库存行失败: %w", err)
		}
		if stocked.Valid {
			item.UpdatedAt = stocked.Time.Truncate(time.Second).Format(time.RFC3339)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 Menu 库存失败: %w", err)
	}
	return items, nil
}

func (r *PostgresRepository) saveInventory(ctx context.Context, userID string, items []InventoryItem) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开始 Menu 库存事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM menu.inventory_items WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("清理旧 Menu 库存失败: %w", err)
	}
	for position, item := range items {
		var stockedAt *time.Time
		if item.UpdatedAt != "" {
			parsed, err := time.Parse(time.RFC3339, item.UpdatedAt)
			if err != nil {
				return fmt.Errorf("库存 %q 的更新时间无效: %w", item.Name, err)
			}
			stockedAt = &parsed
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO menu.inventory_items(user_id, name, quantity, unit, stocked_at, position)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, item.Name, item.Quantity, item.Unit, stockedAt, position); err != nil {
			return fmt.Errorf("写入 Menu 库存失败: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交 Menu 库存事务失败: %w", err)
	}
	return nil
}

func (r *PostgresRepository) loadProfile(ctx context.Context, userID string) (Profile, error) {
	var profile Profile
	err := r.pool.QueryRow(ctx, `
		SELECT baby_name, COALESCE(birth_date::text, ''), allergies, dislikes, notes
		FROM menu.profiles WHERE user_id = $1`, userID).
		Scan(&profile.BabyName, &profile.BirthDate, &profile.Allergies, &profile.Dislikes, &profile.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, nil
	}
	if err != nil {
		return Profile{}, fmt.Errorf("读取 Menu 档案失败: %w", err)
	}
	return profile, nil
}

func (r *PostgresRepository) saveProfile(ctx context.Context, userID string, profile Profile) error {
	var birthDate any
	if profile.BirthDate != "" {
		if _, err := time.Parse("2006-01-02", profile.BirthDate); err != nil {
			return fmt.Errorf("宝宝出生日期 %q 无效", profile.BirthDate)
		}
		birthDate = profile.BirthDate
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO menu.profiles(user_id, baby_name, birth_date, allergies, dislikes, notes)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			baby_name = EXCLUDED.baby_name,
			birth_date = EXCLUDED.birth_date,
			allergies = EXCLUDED.allergies,
			dislikes = EXCLUDED.dislikes,
			notes = EXCLUDED.notes,
			updated_at = now()`,
		userID, profile.BabyName, birthDate, nonNilStrings(profile.Allergies), nonNilStrings(profile.Dislikes), profile.Notes)
	if err != nil {
		return fmt.Errorf("写入 Menu 档案失败: %w", err)
	}
	return nil
}

// UserIDs returns platform users that currently own any Menu business data.
func (r *PostgresRepository) UserIDs(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id::text FROM menu.meals
		UNION SELECT user_id::text FROM menu.inventory_items
		UNION SELECT user_id::text FROM menu.profiles
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("读取 Menu 用户名册失败: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("解析 Menu 用户名册失败: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteUserData is idempotent and is called before account hard deletion.
func (r *PostgresRepository) DeleteUserData(ctx context.Context, userID string) error {
	if !canonicalMenuUserID(userID) {
		return fmt.Errorf("Menu PostgreSQL user ID 必须是规范 UUID")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开始清除 Menu 数据事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, table := range []string{"menu.profiles", "menu.inventory_items", "menu.meals"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id = $1", userID); err != nil {
			return fmt.Errorf("清除 %s 失败: %w", table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交清除 Menu 数据事务失败: %w", err)
	}
	return nil
}

// ImportSnapshotIfEmpty performs the one-time JSON-to-PostgreSQL cutover in a
// single serializable transaction. It never merges with or overwrites existing
// rows: operators must inspect duplicates explicitly instead of guessing which
// copy is authoritative.
func (r *PostgresRepository) ImportSnapshotIfEmpty(ctx context.Context, userID string, days []Day, items []InventoryItem, profile Profile) error {
	if !canonicalMenuUserID(userID) {
		return fmt.Errorf("Menu PostgreSQL user ID 必须是规范 UUID")
	}
	if err := validateImportSnapshot(days, items, profile); err != nil {
		return err
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("开始 Menu 数据迁移事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize two accidental importer invocations for the same target user.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userID); err != nil {
		return fmt.Errorf("锁定 Menu 数据迁移失败: %w", err)
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM account.users WHERE id = $1`, userID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("目标平台用户不存在")
		}
		return fmt.Errorf("检查目标平台用户失败: %w", err)
	}
	if !active {
		return fmt.Errorf("目标平台用户不是 active 状态")
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM menu.meals WHERE user_id = $1)
		    OR EXISTS (SELECT 1 FROM menu.inventory_items WHERE user_id = $1)
		    OR EXISTS (SELECT 1 FROM menu.profiles WHERE user_id = $1)`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("检查目标 Menu 数据失败: %w", err)
	}
	if exists {
		return ErrMenuDataExists
	}

	sortDays(days)
	for _, day := range days {
		for _, mealKind := range mealOrder {
			meal := day.mealOf(mealKind.field)
			if meal == nil {
				continue
			}
			dishList := meal.Dishes
			if dishList == nil {
				dishList = []Dish{}
			}
			dishes, err := json.Marshal(dishList)
			if err != nil {
				return fmt.Errorf("序列化 Menu 菜品失败: %w", err)
			}
			var feedback []byte
			if meal.Feedback != nil {
				feedback, err = json.Marshal(meal.Feedback)
				if err != nil {
					return fmt.Errorf("序列化 Menu 反馈失败: %w", err)
				}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO menu.meals(user_id, meal_date, meal_type, meal_time, dishes, feedback)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				userID, day.Date, mealKind.field, meal.Time, dishes, feedback); err != nil {
				return fmt.Errorf("迁移 Menu 历史失败: %w", err)
			}
		}
	}
	for position, item := range items {
		var stockedAt *time.Time
		if item.UpdatedAt != "" {
			parsed, _ := time.Parse(time.RFC3339, item.UpdatedAt)
			stockedAt = &parsed
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO menu.inventory_items(user_id, name, quantity, unit, stocked_at, position)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, item.Name, item.Quantity, item.Unit, stockedAt, position); err != nil {
			return fmt.Errorf("迁移 Menu 库存失败: %w", err)
		}
	}
	if !profile.IsEmpty() {
		var birthDate any
		if profile.BirthDate != "" {
			birthDate = profile.BirthDate
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO menu.profiles(user_id, baby_name, birth_date, allergies, dislikes, notes)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, profile.BabyName, birthDate, nonNilStrings(profile.Allergies), nonNilStrings(profile.Dislikes), profile.Notes); err != nil {
			return fmt.Errorf("迁移 Menu 档案失败: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交 Menu 数据迁移失败: %w", err)
	}
	return nil
}

func validateImportSnapshot(days []Day, items []InventoryItem, profile Profile) error {
	if len(days) == 0 && len(items) == 0 && profile.IsEmpty() {
		return fmt.Errorf("源目录没有可迁移的 Menu 数据")
	}
	seenInventory := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Name == "" || item.Quantity <= 0 {
			return fmt.Errorf("库存包含无效条目 %q", item.Name)
		}
		if _, duplicate := seenInventory[item.Name]; duplicate {
			return fmt.Errorf("库存包含重复食材 %q", item.Name)
		}
		seenInventory[item.Name] = struct{}{}
		if item.UpdatedAt != "" {
			if _, err := time.Parse(time.RFC3339, item.UpdatedAt); err != nil {
				return fmt.Errorf("库存 %q 的更新时间无效: %w", item.Name, err)
			}
		}
	}
	seenMeals := make(map[string]struct{})
	for _, day := range days {
		if _, err := time.Parse("2006-01-02", day.Date); err != nil {
			return fmt.Errorf("历史日期 %q 无效", day.Date)
		}
		for _, mealKind := range mealOrder {
			if day.mealOf(mealKind.field) == nil {
				continue
			}
			key := day.Date + "\x00" + mealKind.field
			if _, duplicate := seenMeals[key]; duplicate {
				return fmt.Errorf("历史包含重复餐次 %s %s", day.Date, mealKind.field)
			}
			seenMeals[key] = struct{}{}
		}
	}
	if profile.BirthDate != "" {
		if _, err := time.Parse("2006-01-02", profile.BirthDate); err != nil {
			return fmt.Errorf("宝宝出生日期 %q 无效", profile.BirthDate)
		}
	}
	return nil
}

func canonicalMenuUserID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// sortDays is also used by the manual JSON importer before persisting a snapshot.
func sortDays(days []Day) {
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
}
