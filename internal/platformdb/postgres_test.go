package platformdb

import (
	"context"
	"strings"
	"testing"
)

func TestOpenRequiresDatabaseURL(t *testing.T) {
	_, err := Open(context.Background(), "  ")
	if err == nil || !strings.Contains(err.Error(), "PLATFORM_DATABASE_URL") {
		t.Fatalf("Open() error = %v, want missing PLATFORM_DATABASE_URL", err)
	}
}
