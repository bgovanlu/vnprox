package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunGroup_StopsAllWhenOneReturns(t *testing.T) {
	var g runGroup

	started := make(chan struct{}, 2)
	stopped := make(chan struct{}, 2)

	g.add(func(ctx context.Context) error {
		started <- struct{}{}
		return nil // returns immediately
	})
	g.add(func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done() // must be cancelled by the first actor's return
		stopped <- struct{}{}
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- g.run(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return: second actor was not cancelled")
	}

	if len(started) != 2 {
		t.Fatalf("expected both actors to start, got %d", len(started))
	}
	if len(stopped) != 1 {
		t.Fatalf("expected the long-running actor to observe cancellation, got %d signals", len(stopped))
	}
}

func TestRunGroup_PropagatesFirstError(t *testing.T) {
	var g runGroup
	sentinel := errors.New("boom")

	g.add(func(ctx context.Context) error {
		return sentinel
	})
	g.add(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	err := g.run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("run() returned %v, want %v", err, sentinel)
	}
}

func TestRunGroup_ParentCancellationStopsActors(t *testing.T) {
	var g runGroup
	g.add(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
	g.add(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return after parent context cancellation")
	}
}
