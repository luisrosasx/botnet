-- Nodes/leaves minimos para probar la seleccion T-0106 antes de que
-- EP-08 publique su DDL canonico (mismo shape que BLUEPRINT §9 en las
-- columnas que usa la consulta: public_ip, optout_at, reprobe_blocked_until).
-- Debe casar con la DDL canonica 0003 (la fixture se aplica tambien
-- sobre bases ya migradas): todas las columnas NOT NULL de nodes van
-- rellenas y las de leaves ya no existen salvo reprobe_blocked_until.
CREATE TABLE IF NOT EXISTS nodes (
  node_id uuid PRIMARY KEY,
  slug char(12) NOT NULL UNIQUE,
  device_token_hash char(64) NOT NULL UNIQUE,
  arch text NOT NULL,
  device_class text NOT NULL,
  public_ip inet,
  consent_at timestamptz NOT NULL,
  optout_at timestamptz
);
CREATE TABLE IF NOT EXISTS leaves (
  leave_id bigserial PRIMARY KEY,
  node_id uuid NOT NULL REFERENCES nodes (node_id),
  method text NOT NULL,
  reprobe_blocked_until timestamptz NOT NULL
);
-- 5 000 nodos de ruido para que el planner use indices (no seq scan).
INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, public_ip, consent_at)
SELECT md5(i::text)::uuid,
       substr(md5('noise-s' || i::text), 1, 12),
       md5('noise-t' || i::text),
       'mipsle', 'C',
       ('197.' || (i % 250) || '.' || ((i / 250) % 250) || '.' || ((i * 13) % 250))::inet,
       now()
FROM generate_series(1, 5000) i
ON CONFLICT DO NOTHING;
-- casos nominales: opt-out reciente, reprobe bloqueado y reprobe vencido.
INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, public_ip, consent_at, optout_at) VALUES
  ('11111111-1111-4111-8111-111111111101', 'aaaaaaaaaa01', md5('t101'), 'mipsle', 'C', '8.8.4.4'::inet, now(), now() - interval '30 days'),
  ('11111111-1111-4111-8111-111111111102', 'aaaaaaaaaa02', md5('t102'), 'mipsle', 'C', '8.8.5.5'::inet, now(), NULL),
  ('11111111-1111-4111-8111-111111111103', 'aaaaaaaaaa03', md5('t103'), 'mipsle', 'C', '8.8.6.6'::inet, now(), NULL),
  ('11111111-1111-4111-8111-111111111104', 'aaaaaaaaaa04', md5('t104'), 'mipsle', 'C', '8.8.7.7'::inet, now(), NULL)
ON CONFLICT DO NOTHING;
INSERT INTO leaves (node_id, method, reprobe_blocked_until)
SELECT v.n::uuid, v.m, v.t FROM (VALUES
  ('11111111-1111-4111-8111-111111111102', 'http', now() + interval '11 months'),
  ('11111111-1111-4111-8111-111111111103', 'http', now() - interval '1 day')) AS v(n, m, t)
WHERE NOT EXISTS (SELECT 1 FROM leaves l WHERE l.node_id = v.n::uuid);
CREATE INDEX IF NOT EXISTS nodes_public_ip_idx ON nodes (public_ip);
CREATE INDEX IF NOT EXISTS nodes_optout_idx ON nodes (optout_at) WHERE optout_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS leaves_node_idx ON leaves (node_id);
CREATE INDEX IF NOT EXISTS leaves_reprobe_idx ON leaves (reprobe_blocked_until);
ANALYZE;
