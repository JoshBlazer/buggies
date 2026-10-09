package budget

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAllowAfterBudgetExceeded(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spend.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx := context.Background()
	repo := "tj/commander.js"
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	p := Policy{MonthlyLimitCents: 500}

	if ok, _, _ := l.Allow(ctx, repo, p, now); !ok {
		t.Fatal("fresh repo should be allowed")
	}
	if err := l.Add(ctx, repo, 300, now); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := l.Allow(ctx, repo, p, now); !ok {
		t.Fatal("still under budget (300<500) should be allowed")
	}
	if err := l.Add(ctx, repo, 300, now); err != nil { // 600 total
		t.Fatal(err)
	}
	ok, note, err := l.Allow(ctx, repo, p, now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("over budget (600>=500) must be denied")
	}
	if note == "" {
		t.Error("denial must carry a note for the PR")
	}

	// A different month starts fresh.
	next := now.AddDate(0, 1, 0)
	if ok, _, _ := l.Allow(ctx, repo, p, next); !ok {
		t.Error("new month resets the budget")
	}
}

func TestNoCapAlwaysAllows(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "spend.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx := context.Background()
	_ = l.Add(ctx, "r", 1_000_000, time.Now())
	if ok, _, _ := l.Allow(ctx, "r", Policy{MonthlyLimitCents: 0}, time.Now()); !ok {
		t.Error("no cap (limit<=0) must always allow")
	}
}
