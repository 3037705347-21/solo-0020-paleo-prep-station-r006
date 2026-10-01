package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/domain"
)

func mustSpecimen(t *testing.T, id, catalog string) domain.Specimen {
	t.Helper()
	value, err := domain.NewSpecimen(id, catalog, "北坡-07", "", "粉砂岩", "", time.Now())
	if err != nil {
		t.Fatalf("build specimen: %v", err)
	}
	return value
}

func TestMutateCommitsWhenContextActive(t *testing.T) {
	repo := NewMemory()
	value := mustSpecimen(t, "specimen-1", "PPS-0001")
	if err := repo.Mutate(context.Background(), func(m *Mutation) error {
		m.PutSpecimen(value)
		return nil
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if err := repo.Read(func(m *Mutation) error {
		stored, ok := m.GetSpecimen("specimen-1")
		if !ok {
			t.Fatal("committed specimen missing")
		}
		if stored.CatalogNumber != value.CatalogNumber {
			t.Fatalf("stored catalog %q, want %q", stored.CatalogNumber, value.CatalogNumber)
		}
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
}

func TestMutateDiscardsWhenCancelledBeforeCommit(t *testing.T) {
	repo := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	value := mustSpecimen(t, "specimen-1", "PPS-0001")
	err := repo.Mutate(ctx, func(m *Mutation) error {
		m.PutSpecimen(value)
		cancel() // client cancels while the mutation is being applied
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if err := repo.Read(func(m *Mutation) error {
		if _, ok := m.GetSpecimen("specimen-1"); ok {
			t.Fatal("cancelled mutation leaked into repository state")
		}
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
}

func TestMutateKeepsBusinessErrorWhenCancelled(t *testing.T) {
	repo := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	business := errors.New("business rule rejected")
	err := repo.Mutate(ctx, func(m *Mutation) error {
		m.PutSpecimen(mustSpecimen(t, "specimen-1", "PPS-0001"))
		cancel()
		return business
	})
	if !errors.Is(err, business) {
		t.Fatalf("expected business error, got %v", err)
	}
	if err := repo.Read(func(m *Mutation) error {
		if _, ok := m.GetSpecimen("specimen-1"); ok {
			t.Fatal("failed mutation leaked into repository state")
		}
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
}
