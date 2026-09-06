package main

import (
	"context"
	"errors"
	"time"
)

func finishService(result error, steps ...func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, step := range steps {
		result = errors.Join(result, step(ctx))
	}
	return result
}
