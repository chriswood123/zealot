-- Seed data for the shop database (runs once on first postgres container start).
CREATE TABLE IF NOT EXISTS items (
    id    integer PRIMARY KEY,
    name  text NOT NULL,
    price numeric(10,2) NOT NULL
);

INSERT INTO items
SELECT g, 'item-' || g, (random() * 100)::numeric(10,2)
FROM generate_series(1, 100000) AS g
ON CONFLICT DO NOTHING;
