package main

import (
	"context"
	"errors"
	"testing"
)

func TestReadinessDependencyFailures(t *testing.T) {
	for failed := 0; failed < 7; failed++ {
		checks := make([]func(context.Context) error, 7)
		for i := range checks {
			checks[i] = func(context.Context) error {
				if i == failed {
					return errors.New("private failure")
				}
				return nil
			}
		}
		if checkReadiness(context.Background(), checks...) == nil {
			t.Fatalf("dependency %d ignored", failed)
		}
	}
	if err := checkReadiness(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
