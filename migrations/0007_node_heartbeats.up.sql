-- EP-12 T-1202: historico Postgres del latido que leen los paneles PG de
-- deploy/grafana/fleet.json (RSS p99, perdida p99, inventario de
-- versiones). El heartbeat handler inserta una fila por latido decodificado
-- (rss_kib, streams, loss_estimate y agent_version viajan en el frame
-- hbcodec) y actualiza nodes.agent_version desde esa misma fila: el
-- inventario nunca puede ir por delante del alambre.
CREATE TABLE IF NOT EXISTS node_heartbeats (
  node_id uuid NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
  ts timestamptz NOT NULL DEFAULT now(),
  seq bigint NOT NULL DEFAULT 0,
  rss_kib integer NOT NULL DEFAULT 0,
  streams integer NOT NULL DEFAULT 0,
  loss_estimate integer NOT NULL DEFAULT 0,
  agent_version text);
-- $__timeFilter(ts) de los paneles temporales.
CREATE INDEX IF NOT EXISTS node_heartbeats_ts_idx ON node_heartbeats (ts);
-- JOIN LATERAL de frescura: ultimo latido por nodo.
CREATE INDEX IF NOT EXISTS node_heartbeats_node_ts_idx ON node_heartbeats (node_id, ts DESC);
