CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE shows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    price_paise BIGINT NOT NULL CHECK (price_paise >= 0),
    per_user_limit INT NOT NULL DEFAULT 4 CHECK (per_user_limit > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE seats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    show_id UUID NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
    seat_number TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'available'
        CHECK (status IN ('available', 'held', 'confirmed')),
    user_id TEXT,
    hold_expires_at TIMESTAMPTZ,
    UNIQUE (show_id, seat_number)
);

CREATE INDEX idx_seats_show_id ON seats(show_id);

CREATE TABLE reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    show_id UUID NOT NULL REFERENCES shows(id),
    user_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL
        CHECK (status IN ('held', 'confirmed', 'cancelled')),
    amount_paise BIGINT NOT NULL CHECK (amount_paise >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (show_id, user_id, idempotency_key)
);

CREATE TABLE reservation_seats (
    reservation_id UUID NOT NULL REFERENCES reservations(id) ON DELETE CASCADE,
    seat_id UUID NOT NULL REFERENCES seats(id),
    PRIMARY KEY (reservation_id, seat_id)
);

CREATE TABLE show_user_limits (
    show_id UUID NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    reserved_count INT NOT NULL DEFAULT 0 CHECK (reserved_count >= 0),
    PRIMARY KEY (show_id, user_id)
);