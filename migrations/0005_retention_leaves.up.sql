-- 0005_retention_leaves.up.sql — EP-10 T-1003 (A2-07) y T-1007 (A3-14).
-- Retencion: el grano minuto vive solo en sessions (30 dias) y la
-- tabla contable usage es diaria (12 meses); los indices soportan el
-- purga horaria y la reconstruccion del cobro disputado.
CREATE INDEX IF NOT EXISTS sessions_purge_started
    ON sessions (started_at) WHERE ended_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS usage_day
    ON usage (day);

-- Memoria del opt-out: la consulta de descubrimiento (T-0106) excluye
-- IPs con leave.reprobe_blocked_until futuro; el guard de CI
-- (make reprobe-guard) audita que nadie re-cluto con optout_at reciente.
CREATE INDEX IF NOT EXISTS leaves_reprobe_until
    ON leaves (reprobe_blocked_until);

CREATE INDEX IF NOT EXISTS nodes_optout_dated
    ON nodes (optout_at) WHERE optout_at IS NOT NULL;
