package store

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/sandbox"
)

func sample(id string, conf finding.Confidence, imp finding.Impact) finding.Finding {
	return finding.Finding{
		ID: id, Type: finding.Correctness, Confidence: conf, Impact: imp, State: finding.Verified,
		Claim:    "gold tier pays full price",
		Location: finding.Location{Repo: "acme/shop", Path: "src/discount.ts", Symbol: "applyDiscount", Line: 4},
		Proof: &finding.Proof{
			Test:   sandbox.Artifact{Path: "src/__buggies__/discount.test.ts", Content: []byte("expect(80)")},
			Result: sandbox.Result{Outcome: sandbox.Failed, Reproducible: true},
		},
	}
}

func TestRoundTripIsEncryptedAtRest(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "findings.db")
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	f := sample("f_1", finding.Reproduced, finding.DataLoss)
	if err := s.Put(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "f_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Claim != f.Claim || string(got.Proof.Test.Content) != "expect(80)" || !got.Proof.Result.Reproducible {
		t.Fatalf("round trip lost data: %+v", got)
	}

	// The claim and the test must not be readable from the raw database.
	var blob []byte
	if err := s.db.QueryRow(`SELECT blob FROM findings WHERE id = 'f_1'`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("gold tier")) || bytes.Contains(blob, []byte("expect(80)")) {
		t.Fatal("finding stored in the clear")
	}

	// Reopening with the on-disk key file still decrypts.
	s.Close()
	s2, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Get(ctx, "f_1"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

func TestListRanksVerificationBeforeSeverity(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.Put(ctx, sample("unverified-critical", finding.Unverified, finding.Exploitable))
	_ = s.Put(ctx, sample("reproduced-medium", finding.Reproduced, finding.Degraded))
	_ = s.Put(ctx, sample("reproduced-dataloss", finding.Reproduced, finding.DataLoss))
	got, err := s.List(ctx, finding.Query{Repo: "acme/shop"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"reproduced-dataloss", "reproduced-medium", "unverified-critical"}
	for i, w := range want {
		if got[i].ID != w {
			t.Fatalf("order = %v want %v", ids(got), want)
		}
	}
}

func TestRerunNeverDemotesLifecycle(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Put(ctx, sample("f_1", finding.Reproduced, finding.DataLoss)); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, "f_1", finding.Dismissed, "false positive per reviewer"); err != nil {
		t.Fatal(err)
	}
	// Same finding shows up again on a re-run of the PR.
	if err := s.Put(ctx, sample("f_1", finding.Reproduced, finding.DataLoss)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "f_1")
	if got.State != finding.Dismissed {
		t.Fatalf("re-run demoted state to %s", got.State)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM transitions WHERE id = 'f_1'`).Scan(&n); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("transitions = %d", n)
	}
	d, tot, err := s.DismissalRate(ctx, "acme/shop", finding.Correctness)
	if err != nil || d != 1 || tot != 1 {
		t.Fatalf("dismissal rate = %d/%d err=%v", d, tot, err)
	}
}

func ids(fs []finding.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}
