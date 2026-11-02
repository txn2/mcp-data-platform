-- The target database of the postgres connection kind (#2400): a small order
-- system the acceptance suite reads and writes through postgres_query and
-- postgres_execute. Applied once, on the first start of a fresh volume;
-- `make dev-down` removes the volume.

CREATE TABLE customers (
  id         serial PRIMARY KEY,
  email      text NOT NULL UNIQUE,
  name       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  customer_id int NOT NULL REFERENCES customers (id),
  total       numeric(14, 4) NOT NULL CHECK (total >= 0),
  placed_at   timestamptz NOT NULL,
  ship_on     date,
  tags        text[] NOT NULL DEFAULT '{}',
  attrs       jsonb NOT NULL DEFAULT '{}'
);

-- One row of every type the kind maps, read by the type-mapping criterion.
CREATE TABLE type_samples (
  id        int PRIMARY KEY,
  small     int2,
  big       int8,
  amount    numeric(30, 10),
  ratio     float8,
  at        timestamptz,
  local_at  timestamp,
  day       date,
  doc       jsonb,
  raw       json,
  uid       uuid,
  labels    text[],
  counts    int4[],
  blob      bytea,
  flag      bool,
  note      text
);

INSERT INTO customers (email, name, created_at) VALUES
  ('ada@acme.example', 'Ada Lovelace', '2026-01-05T09:00:00Z'),
  ('grace@acme.example', 'Grace Hopper', '2026-02-11T14:30:00Z');

INSERT INTO orders (customer_id, total, placed_at, ship_on, tags, attrs) VALUES
  (1, 1299.5000, '2026-03-01T10:15:00Z', '2026-03-04', '{priority}', '{"channel": "web"}'),
  (2, 42.0000, '2026-03-02T16:40:00Z', NULL, '{}', '{"channel": "phone", "gift": true}');

INSERT INTO type_samples VALUES (
  1, 7, 9007199254740993, 12345678901234567890.0123456789, 0.25,
  '2026-11-01T12:30:00+02:00', '2026-11-01T08:00:00', '2026-11-01',
  '{"a": [1, 2], "b": {"c": null}}', '[true, "x"]',
  '6f1c2c1e-7d2b-4b9e-9a51-0c4f0d1e2a3b', '{x,"y z"}', '{1,NULL,3}',
  '\x000102ff', true, NULL
);
