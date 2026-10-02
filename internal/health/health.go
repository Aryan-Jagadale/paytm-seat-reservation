package health

import (
    "context"
    "fmt"
    "time"
)

type DBChecker interface {
    Ping(ctx context.Context) error
}

type Checker struct {
    db      DBChecker
    timeout time.Duration
}

func NewChecker(db DBChecker, timeout time.Duration) *Checker {
    return &Checker{
        db:      db,
        timeout: timeout,
    }
}

func (c *Checker) Ready(ctx context.Context) error {
    pingCtx, cancel := context.WithTimeout(ctx, c.timeout)
    defer cancel()

    if err := c.db.Ping(pingCtx); err != nil {
        return fmt.Errorf("database not ready: %w", err)
    }

    return nil
}