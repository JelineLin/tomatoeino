BEGIN;

CREATE SCHEMA IF NOT EXISTS menu;

CREATE TABLE menu.meals (
    user_id    UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    meal_date  DATE NOT NULL,
    meal_type  TEXT NOT NULL CHECK (meal_type IN ('lunch', 'fruit', 'dinner')),
    meal_time  TEXT NOT NULL DEFAULT '',
    dishes     JSONB NOT NULL CHECK (jsonb_typeof(dishes) = 'array'),
    feedback   JSONB CHECK (feedback IS NULL OR jsonb_typeof(feedback) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, meal_date, meal_type)
);
CREATE INDEX meals_user_date_idx ON menu.meals(user_id, meal_date DESC);

CREATE TABLE menu.inventory_items (
    user_id    UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    quantity   DOUBLE PRECISION NOT NULL CHECK (quantity > 0),
    unit       TEXT NOT NULL DEFAULT '份',
    stocked_at TIMESTAMPTZ,
    position   INTEGER NOT NULL CHECK (position >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, name),
    UNIQUE (user_id, position)
);

CREATE TABLE menu.profiles (
    user_id    UUID PRIMARY KEY REFERENCES account.users(id) ON DELETE CASCADE,
    baby_name  TEXT NOT NULL DEFAULT '',
    birth_date DATE,
    allergies  TEXT[] NOT NULL DEFAULT '{}',
    dislikes   TEXT[] NOT NULL DEFAULT '{}',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
