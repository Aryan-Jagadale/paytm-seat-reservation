package config

import (
    "fmt"
    "os"
    "strconv"
    "time"
)

type Config struct {
    Port            string
    DatabaseURL     string
    MaxConns        int32
    MinConns        int32
    DBConnectTimeout time.Duration
    ShutdownTimeout time.Duration
}

func Load() (Config, error) {
    cfg := Config{
        Port:             getEnv("PORT", "8080"),
        DatabaseURL:      os.Getenv("DATABASE_URL"),
        MaxConns:         10,
        MinConns:         2,
        DBConnectTimeout: 5 * time.Second,
        ShutdownTimeout: 10 * time.Second,
    }

    if cfg.DatabaseURL == "" {
        return Config{}, fmt.Errorf("DATABASE_URL is required")
    }

    maxConns, err := getInt32("MAX_CONNS", cfg.MaxConns)
    if err != nil {
        return Config{}, fmt.Errorf("MAX_CONNS: %w", err)
    }

    minConns, err := getInt32("MIN_CONNS", cfg.MinConns)
    if err != nil {
        return Config{}, fmt.Errorf("MIN_CONNS: %w", err)
    }

    connectTimeout, err := getDuration(
        "DB_CONNECT_TIMEOUT",
        cfg.DBConnectTimeout,
    )
    if err != nil {
        return Config{}, fmt.Errorf("DB_CONNECT_TIMEOUT: %w", err)
    }

    shutdownTimeout, err := getDuration(
        "SHUTDOWN_TIMEOUT",
        cfg.ShutdownTimeout,
    )
    if err != nil {
        return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
    }

    if maxConns < 1 {
        return Config{}, fmt.Errorf("MAX_CONNS must be >= 1")
    }
    if minConns < 0 || minConns > maxConns {
        return Config{}, fmt.Errorf(
            "MIN_CONNS must be between 0 and MAX_CONNS",
        )
    }
    if connectTimeout <= 0 || shutdownTimeout <= 0 {
        return Config{}, fmt.Errorf("timeouts must be positive")
    }

    if _, err := strconv.Atoi(cfg.Port); err != nil {
        return Config{}, fmt.Errorf("PORT must be numeric: %w", err)
    }

    cfg.MaxConns = maxConns
    cfg.MinConns = minConns
    cfg.DBConnectTimeout = connectTimeout
    cfg.ShutdownTimeout = shutdownTimeout

    return cfg, nil
}

func getEnv(key, fallback string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return fallback
}

func getInt32(key string, fallback int32) (int32, error) {
    value := os.Getenv(key)
    if value == "" {
        return fallback, nil
    }

    parsed, err := strconv.ParseInt(value, 10, 32)
    if err != nil {
        return 0, fmt.Errorf("invalid integer: %w", err)
    }

    return int32(parsed), nil
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
    value := os.Getenv(key)
    if value == "" {
        return fallback, nil
    }

    parsed, err := time.ParseDuration(value)
    if err != nil {
        return 0, fmt.Errorf("invalid duration: %w", err)
    }

    return parsed, nil
}