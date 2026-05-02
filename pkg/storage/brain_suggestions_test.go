package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestBrainSuggestion_InsertGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.InsertBrainSuggestion(ctx, "saarsena", "1brl", "abc123", "use snake_case for GDScript methods", "saw a rename in this push")
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetBrainSuggestion(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Owner != "saarsena" || got.Repo != "1brl" || got.AfterSHA != "abc123" {
		t.Errorf("identity fields: %+v", got)
	}
	if got.Text != "use snake_case for GDScript methods" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.Reason != "saw a rename in this push" {
		t.Errorf("Reason = %q", got.Reason)
	}
	if got.Status != BrainStatusPending {
		t.Errorf("Status = %q, want pending", got.Status)
	}
	if got.DecidedAt != nil {
		t.Errorf("DecidedAt = %v, want nil", got.DecidedAt)
	}
}

func TestBrainSuggestion_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetBrainSuggestion(context.Background(), 999)
	if !errors.Is(err, ErrBrainSuggestionNotFound) {
		t.Errorf("expected ErrBrainSuggestionNotFound, got %v", err)
	}
}

func TestBrainSuggestion_List(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	idA, _ := s.InsertBrainSuggestion(ctx, "o", "r1", "sha1", "one", "")
	idB, _ := s.InsertBrainSuggestion(ctx, "o", "r2", "sha2", "two", "")
	idC, _ := s.InsertBrainSuggestion(ctx, "o", "r3", "sha3", "three", "")

	if err := s.DecideBrainSuggestion(ctx, idB, BrainStatusAccepted); err != nil {
		t.Fatalf("Decide accepted: %v", err)
	}
	if err := s.DecideBrainSuggestion(ctx, idC, BrainStatusRejected); err != nil {
		t.Fatalf("Decide rejected: %v", err)
	}

	pending, err := s.ListBrainSuggestions(ctx, BrainStatusPending)
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != idA {
		t.Errorf("pending = %+v, want only id %d", pending, idA)
	}

	all, err := s.ListBrainSuggestions(ctx, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len(all) = %d, want 3", len(all))
	}

	accepted, err := s.ListBrainSuggestions(ctx, BrainStatusAccepted)
	if err != nil {
		t.Fatalf("List accepted: %v", err)
	}
	if len(accepted) != 1 || accepted[0].ID != idB {
		t.Errorf("accepted = %+v", accepted)
	}
	if accepted[0].DecidedAt == nil {
		t.Error("accepted row DecidedAt should be set")
	}
}

func TestBrainSuggestion_Decide_GuardsDoubleApply(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	id, _ := s.InsertBrainSuggestion(ctx, "o", "r", "sha", "x", "")

	if err := s.DecideBrainSuggestion(ctx, id, BrainStatusAccepted); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Second call must fail — the row is no longer pending.
	err := s.DecideBrainSuggestion(ctx, id, BrainStatusAccepted)
	if err == nil {
		t.Error("expected error on double-accept, got nil")
	}
}

func TestBrainSuggestion_Decide_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	err := s.DecideBrainSuggestion(context.Background(), 1, "approved")
	if err == nil {
		t.Error("expected error on invalid status, got nil")
	}
}
