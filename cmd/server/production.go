//go:build production

package main

import (
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func parseDevelopment([]string, func(string) string) (Config, error) {
	return Config{}, errors.New("unsupported mode")
}
func validateDevelopment(Config) error { return errors.New("unsupported mode") }
func prepareDevelopment(c *Config) (func(), error) {
	if c.Development {
		return func() {}, errors.New("unsupported mode")
	}
	return func() {}, nil
}
func seedDevelopment(context.Context, *store.Store) error { return errors.New("unsupported mode") }
