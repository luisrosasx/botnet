-- EP-08 T-0801: indices nombrados del blueprint §9. Cada indice responde
-- una pregunta contable o de seleccion concreta; los parciales dejan
-- claro que "un secreto vivo por nodo" y "keys activas del tenant" son
-- invariantes del motor, no cortesias de la app.

-- api_keys: validar/warmup solo de keys activas (T-0804, A3-07).
CREATE INDEX api_keys_active_tenant ON api_keys (tenant_id) WHERE status = 'active';

-- nodes: "quien lleva tiempo sin heartbeat" (watchdog, T-05xx) y
-- barridos por estado con orden de frescura.
CREATE INDEX nodes_status_seen ON nodes (status, last_seen_at);

-- nodes: seleccion de exit por geo del cliente (T-0805, §10).
CREATE INDEX nodes_geo ON nodes (country, asn, city);

-- nodes: dueños en suelo legal, excluidos del re-clutaje.
CREATE INDEX nodes_optout ON nodes (optout_at) WHERE optout_at IS NULL;

-- node_secrets: el invariante A3-06 — exactamente un secreto vivo por
-- nodo; insertar un segundo secreto vivo falla por el motor.
CREATE UNIQUE INDEX node_secrets_live_per_node ON node_secrets (node_id) WHERE revoked_at IS NULL;

-- sessions: "que ha hecho este nodo" y "sesiones abiertas del tenant".
CREATE INDEX sessions_node_started ON sessions (node_id, started_at);
CREATE INDEX sessions_tenant_open ON sessions (tenant_id, started_at) WHERE ended_at IS NULL;

-- leaves: historia de bajas del nodo y puerta legal por leave_code.
CREATE INDEX leaves_node_left ON leaves (node_id, left_at);
CREATE INDEX leaves_code ON leaves (leave_code);
