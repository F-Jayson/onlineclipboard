// Package config contains only configuration consumed by the current skeleton.
package config

import (
	"fmt"
	"net"
	"os"
)

type Config struct {
	HTTPAddr string
}

func Load() (Config, error) {
	address := os.Getenv("CLIP_HTTP_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return Config{}, fmt.Errorf("CLIP_HTTP_ADDR must be host:port: %w", err)
	}
	return Config{HTTPAddr: address}, nil
}
