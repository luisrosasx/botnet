-- EP-01 descubrimiento: tabla de inventario discovered (T-0101).
-- El suelo reserva 0001_init; esta migracion es la 0002 porque el
-- runner embebido exige sufijo .up.sql y orden lexicografico
-- (migrations/migrations.go). DDL solo adelante, sin ORM (BLUEPRINT §9).

CREATE TABLE discovered (
  ip inet NOT NULL,
  port smallint NOT NULL CHECK (port BETWEEN 1 AND 65535),
  service text,
  vendor text,
  model text,
  asn int,
  country char(2),
  device_class_guess text
    CHECK (device_class_guess IN ('router','camera','nas','unknown')),
  banner_hash char(64),
  source text NOT NULL DEFAULT 'unknown',
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT discovered_ip_port_key UNIQUE (ip, port)
);

-- Comentario de seleccion para el runbook: candidatos frescos con
-- servicio inferido, en orden de frescura -> indice parcial (T-0106).
CREATE INDEX discovered_candidate_pick
  ON discovered (last_seen_at DESC)
  WHERE service IS NOT NULL;

-- Sonda por (asn, pais) para el informe T-0105 cuando la tabla crece.
CREATE INDEX discovered_asn_country ON discovered (asn, country);
