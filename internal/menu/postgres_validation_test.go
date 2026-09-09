package menu

import "testing"

func TestValidateImportSnapshot(t *testing.T) {
	valid := []Day{{Date: "2026-09-09", Lunch: &Meal{Dishes: []Dish{{Name: "粥"}}}}}
	if err := validateImportSnapshot(valid, nil, Profile{}); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if err := validateImportSnapshot(nil, nil, Profile{}); err == nil {
		t.Fatal("empty snapshot should be rejected")
	}
	if err := validateImportSnapshot([]Day{{Date: "09/09/2026", Lunch: &Meal{}}}, nil, Profile{}); err == nil {
		t.Fatal("invalid history date should be rejected")
	}
	if err := validateImportSnapshot(nil, []InventoryItem{{Name: "蛋", Quantity: 1}, {Name: "蛋", Quantity: 2}}, Profile{}); err == nil {
		t.Fatal("duplicate inventory should be rejected")
	}
	if err := validateImportSnapshot(nil, []InventoryItem{{Name: "蛋", Quantity: -1}}, Profile{}); err == nil {
		t.Fatal("negative inventory should be rejected")
	}
}
