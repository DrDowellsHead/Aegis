package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPacerValidation(t *testing.T) {
	for _, invalid := range []int{-1, 0, 601} {
		if _, err := NewPacer(invalid); err == nil {
			t.Fatalf("accepted delivery rate %d", invalid)
		}
	}
	if _, err := NewPacer(60); err != nil {
		t.Fatal(err)
	}
}

func TestPacerIdleAndCanceledWait(t *testing.T) {
	pacer, err := NewPacer(60)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("idle worker must not wait for a delivery slot")
	}
	pacer.NoteAttempt()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	start = time.Now()
	if err := pacer.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancelable wait, got %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("pacer ignored cancellation")
	}
	// A canceled waiter must not consume another permit.
	pacer.next = time.Now().Add(-time.Millisecond)
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
