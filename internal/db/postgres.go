package db

import (
    "context"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)

type PoolConfig struct {
    URL            string
    MaxConns       int32
    MinConns       int32
    ConnectTimeout time.Duration
}

func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
    parsed, err := pgxpool.ParseConfig(cfg.URL)
    if err != nil {
        return nil, fmt.Errorf("parse database URL: %w", err)
    }

    parsed.MaxConns = cfg.MaxConns
    parsed.MinConns = cfg.MinConns
    parsed.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

    pool, err := pgxpool.NewWithConfig(ctx, parsed)
    if err != nil {
        return nil, fmt.Errorf("create database pool: %w", err)
    }

    pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
    defer cancel()

    if err := pool.Ping(pingCtx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("initial database ping: %w", err)
    }

    return pool, nil
}