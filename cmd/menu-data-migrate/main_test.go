package main

import "testing"

func TestSafePathComponent(t *testing.T) {
	for _, value := range []string{"home", "legacy_user-1", "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"} {
		if !safePathComponent(value) {
			t.Errorf("safePathComponent(%q) = false", value)
		}
	}
	for _, value := range []string{"", ".", "..", "../home", "a/b", `a\b`} {
		if safePathComponent(value) {
			t.Errorf("safePathComponent(%q) = true", value)
		}
	}
}
