package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type HTTPConfig struct {
	Addr string
}

type DatabaseConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
	QueryTimeout    time.Duration
}

type Config struct {
	HTTP            HTTPConfig
	Database        DatabaseConfig
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// Load reads the service configuration from the environment. Every field is
// required: a missing or malformed variable fails the load with all problems
// listed at once, instead of falling back to a zero value.
func Load() (Config, error) {
	l := &loader{}

	cfg := Config{
		HTTP: HTTPConfig{
			Addr: l.string("HTTP_ADDR"),
		},
		Database: DatabaseConfig{
			URL:             l.string("DATABASE_URL"),
			MaxConns:        l.int32("DATABASE_MAX_CONNS"),
			MinConns:        l.int32("DATABASE_MIN_CONNS"),
			MaxConnLifetime: l.duration("DATABASE_MAX_CONN_LIFETIME"),
			ConnectTimeout:  l.duration("DATABASE_CONNECT_TIMEOUT"),
			QueryTimeout:    l.duration("DATABASE_QUERY_TIMEOUT"),
		},
		LogLevel:        l.logLevel("LOG_LEVEL"),
		ShutdownTimeout: l.duration("SHUTDOWN_TIMEOUT"),
	}
	if cfg.Database.MaxConns <= 0 {
		l.fail(fmt.Errorf("DATABASE_MAX_CONNS must be positive"))
	}
	if cfg.Database.MinConns < 0 || cfg.Database.MinConns > cfg.Database.MaxConns {
		l.fail(fmt.Errorf("DATABASE_MIN_CONNS must be between 0 and DATABASE_MAX_CONNS"))
	}
	if cfg.Database.MaxConnLifetime <= 0 {
		l.fail(fmt.Errorf("DATABASE_MAX_CONN_LIFETIME must be positive"))
	}
	if cfg.Database.ConnectTimeout <= 0 {
		l.fail(fmt.Errorf("DATABASE_CONNECT_TIMEOUT must be positive"))
	}
	if cfg.Database.QueryTimeout <= 0 {
		l.fail(fmt.Errorf("DATABASE_QUERY_TIMEOUT must be positive"))
	}
	if cfg.ShutdownTimeout <= 0 {
		l.fail(fmt.Errorf("SHUTDOWN_TIMEOUT must be positive"))
	}

	if err := l.err(); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

type loader struct {
	errs []error
}

func (l *loader) fail(err error) {
	l.errs = append(l.errs, err)
}

func (l *loader) err() error {
	return errors.Join(l.errs...)
}

func (l *loader) string(key string) string {
	v := os.Getenv(key)
	if v == "" {
		l.fail(fmt.Errorf("%s is required", key))
	}
	return v
}

func (l *loader) duration(key string) time.Duration {
	v := l.string(key)
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(fmt.Errorf("%s: invalid duration %q: %w", key, v, err))
		return 0
	}
	return d
}

func (l *loader) int32(key string) int32 {
	v := l.string(key)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		l.fail(fmt.Errorf("%s: invalid integer %q: %w", key, v, err))
		return 0
	}
	return int32(n)
}

func (l *loader) logLevel(key string) slog.Level {
	v := l.string(key)
	if v == "" {
		return slog.LevelInfo
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		l.fail(fmt.Errorf("%s: invalid log level %q: %w", key, v, err))
		return slog.LevelInfo
	}
	return level
}
