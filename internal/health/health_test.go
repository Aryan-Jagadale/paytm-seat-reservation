package health

import (
    "context"
    "errors"
    "testing"
    "time"
)

type fakeDB struct {
    err error
}

func (f fakeDB) Ping(ctx context.Context) error {
    return f.err
}

func TestReadiness(t *testing.T) {
    tests := []struct {
        name    string
        dbErr   error
        wantErr bool
    }{
        {
            name:    "database available",
            dbErr:   nil,
            wantErr: false,
        },
        {
            name:    "database unavailable",
            dbErr:   errors.New("connection refused"),
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            checker := NewChecker(
                fakeDB{err: tt.dbErr},
                time.Second,
            )

            err := checker.Ready(context.Background())

            if (err != nil) != tt.wantErr {
                t.Fatalf(
                    "Ready() error = %v, wantErr %v",
                    err,
                    tt.wantErr,
                )
            }
        })
    }
}