package main

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestReadinessExecutesChromeAndCIIValidator(t *testing.T) {
	if err := validateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := validateRuntime(context.Background()); err == nil {
		t.Fatal("missing Java accepted")
	}
}

func TestReadinessRejectsBrokenChrome(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INVOICE_CHROME", executable)
	if validateRuntime(context.Background()) == nil {
		t.Fatal("nonfunctional Chrome accepted")
	}
}

func TestCleanupFailuresRemainFailures(t *testing.T) {
	stopped := 0
	err := finishService(nil, func(context.Context) error { stopped++; return context.DeadlineExceeded }, func(context.Context) error { stopped++; return errors.New("checkpoint failed") })
	if err == nil || stopped != 2 {
		t.Fatalf("cleanup errors lost: %v %d", err, stopped)
	}
	if finishService(nil, func(context.Context) error { return nil }) != nil {
		t.Fatal("clean stop failed")
	}
}
