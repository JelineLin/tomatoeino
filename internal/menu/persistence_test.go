package menu

import (
	"errors"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC) }

func TestPersistentStoresRollBackMemoryWhenWriteFails(t *testing.T) {
	writeErr := errors.New("database unavailable")

	history := &HistoryStore{
		days:    []Day{{Date: "2026-09-08", Lunch: &Meal{Dishes: []Dish{{Name: "粥"}}}}},
		persist: func([]Day) error { return writeErr },
	}
	if _, _, err := history.SetMeal("2026-09-09", "lunch", Meal{Dishes: []Dish{{Name: "面"}}}); !errors.Is(err, writeErr) {
		t.Fatalf("SetMeal() error = %v", err)
	}
	if got := history.Snapshot(); len(got) != 1 || got[0].Date != "2026-09-08" {
		t.Fatalf("history changed after failed persistence: %+v", got)
	}

	inventory := &InventoryStore{
		items:   []InventoryItem{{Name: "鳕鱼", Quantity: 1, Unit: "块"}},
		now:     fixedNow,
		persist: func([]InventoryItem) error { return writeErr },
	}
	if _, err := inventory.Add("鳕鱼", 1, ""); !errors.Is(err, writeErr) {
		t.Fatalf("Add() error = %v", err)
	}
	if got := inventory.List(""); len(got) != 1 || got[0].Quantity != 1 {
		t.Fatalf("inventory changed after failed persistence: %+v", got)
	}

	profiles := &ProfileStore{
		p:       Profile{BabyName: "原名"},
		persist: func(Profile) error { return writeErr },
	}
	if err := profiles.Set(Profile{BabyName: "新名"}); !errors.Is(err, writeErr) {
		t.Fatalf("Set() error = %v", err)
	}
	if got := profiles.Get(); got.BabyName != "原名" {
		t.Fatalf("profile changed after failed persistence: %+v", got)
	}
}

func TestDisabledStoresRejectWrites(t *testing.T) {
	history := &HistoryStore{}
	history.Disable()
	if _, _, err := history.SetMeal("2026-09-09", "lunch", Meal{}); err == nil {
		t.Fatal("disabled history accepted a write")
	}

	inventory := &InventoryStore{now: fixedNow}
	inventory.Disable()
	if _, err := inventory.Add("鸡蛋", 1, "个"); err == nil {
		t.Fatal("disabled inventory accepted a write")
	}

	profiles := &ProfileStore{}
	profiles.Disable()
	if err := profiles.Set(Profile{BabyName: "x"}); err == nil {
		t.Fatal("disabled profile accepted a write")
	}
}
