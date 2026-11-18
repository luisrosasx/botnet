-- EP-15: esquema del servicio de exploracion y reclutamiento (BLUEPRINT
-- docs/recruit/BLUEPRINT.md §6). Once tablas propias; `nodes` NO se toca
-- para no tomar ACCESS EXCLUSIVE sobre la tabla caliente del heartbeat.
-- Todo CREATE lleva IF NOT EXISTS: la migracion se puede reejecutar y el
-- test la aplica dos veces. `SET LOCAL` funciona porque el runner envuelve
-- el fichero entero en una transaccion.
SET LOCAL lock_timeout='2s';
SET LOCAL statement_timeout='60s';

CREATE TABLE IF NOT EXISTS recruit_candidates (
  candidate_id     bigserial PRIMARY KEY,
  ip               inet NOT NULL,
  port             smallint NOT NULL CHECK (port BETWEEN 1 AND 65535),
  state            text NOT NULL DEFAULT 'new' CHECK (state IN (
                     'new','excluded','eligible','sweeping','bannered','assessed',
                     'staging','enrolling','enrolled','verified','deferred',
                     'discarded','burned','rolled_back')),
  source           text NOT NULL DEFAULT 'discovered'
                     CHECK (source IN ('discovered','masscan','shodan','manual')),
  -- Sin FK a `discovered`: esa tabla no tiene clave primaria (solo UNIQUE
  -- (ip,port)) y su purga no debe arrastrar el estado de reclutamiento
  -- (ON DELETE SET NULL rompe con ip/port NOT NULL). El enlace es por valor.
  discovered_id    bigint,
  device_class_guess text,
  device_class     char(1) CHECK (device_class IN ('A','B','C')),
  model_guess      text,
  asn              int,
  country          char(2),
  cred_protocol    text CHECK (cred_protocol IN ('http','ssh','telnet','ftp')),
  cred_ref         text,
  node_id          uuid REFERENCES nodes ON DELETE SET NULL,
  verified_at      timestamptz,
  stage_attempts   smallint NOT NULL DEFAULT 0 CHECK (stage_attempts BETWEEN 0 AND 8),
  age_clock_at     timestamptz NOT NULL DEFAULT now(),
  next_attempt_at  timestamptz NOT NULL DEFAULT now(),
  lease_owner      text,
  lease_expires_at timestamptz,
  lease_token      bigint NOT NULL DEFAULT 0,
  fail_reason      text CHECK (fail_reason IS NULL OR fail_reason IN (
                     'excluded','no_answer','no_banner','no_cred','no_delivery',
                     'flash_cap','age','socks_fail','ip_mismatch','leave','burned')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT recruit_candidate_key UNIQUE (ip, port),
  CONSTRAINT recruit_verified_requires_node
    CHECK (state <> 'verified' OR (node_id IS NOT NULL AND verified_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS recruit_q_claim
  ON recruit_candidates (next_attempt_at) WHERE state IN ('new','eligible','deferred');
CREATE INDEX IF NOT EXISTS recruit_q_lease
  ON recruit_candidates (lease_expires_at) WHERE lease_expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS recruit_q_state ON recruit_candidates (state);
CREATE INDEX IF NOT EXISTS recruit_q_node
  ON recruit_candidates (node_id) WHERE node_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS recruit_q_verified
  ON recruit_candidates (verified_at) WHERE state = 'verified';

CREATE TABLE IF NOT EXISTS recruit_candidate_events (
  event_id     bigserial PRIMARY KEY,
  candidate_id bigint NOT NULL REFERENCES recruit_candidates ON DELETE CASCADE,
  seq          smallint NOT NULL,
  from_state   text NOT NULL,
  to_state     text NOT NULL,
  reason       text,
  actor        text NOT NULL,
  at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT recruit_event_key UNIQUE (candidate_id, seq)
);

CREATE TABLE IF NOT EXISTS recruit_windows (
  window_start   timestamptz PRIMARY KEY,
  rate_class     smallint NOT NULL DEFAULT 1 CHECK (rate_class BETWEEN 0 AND 4),
  unit           text NOT NULL DEFAULT 'pps' CHECK (unit IN ('pps','conn','attempt')),
  pps_budget     int NOT NULL CHECK (pps_budget >= 0),
  pps_used       int NOT NULL DEFAULT 0,
  pps_granted    int NOT NULL DEFAULT 0,
  pps_debt       int NOT NULL DEFAULT 0,
  conn_budget    int NOT NULL CHECK (conn_budget >= 0),
  conn_used      int NOT NULL DEFAULT 0,
  attempt_budget int NOT NULL CHECK (attempt_budget >= 0),
  attempt_used   int NOT NULL DEFAULT 0,
  closed_at      timestamptz,
  CONSTRAINT recruit_budget_debt CHECK (pps_used - pps_budget <= 5000)
);

CREATE INDEX IF NOT EXISTS recruit_windows_open
  ON recruit_windows (window_start DESC) WHERE closed_at IS NULL;

CREATE TABLE IF NOT EXISTS recruit_window_policy (
  hour_utc   smallint PRIMARY KEY CHECK (hour_utc BETWEEN 0 AND 23),
  rate_class smallint NOT NULL CHECK (rate_class BETWEEN 0 AND 4),
  enabled    boolean NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS recruit_control (
  id               smallint PRIMARY KEY CHECK (id = 1),
  paused           boolean NOT NULL DEFAULT false,
  rate_class       smallint NOT NULL DEFAULT 1 CHECK (rate_class BETWEEN 0 AND 4),
  manual_rate_class smallint CHECK (manual_rate_class BETWEEN 0 AND 4),
  tick_at          timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recruit_jobs (
  job_id          bigserial PRIMARY KEY,
  candidate_id    bigint NOT NULL REFERENCES recruit_candidates ON DELETE CASCADE,
  stage           text NOT NULL CHECK (stage IN ('sweep','banner','cred','stage','verify','deliver')),
  attempt         smallint NOT NULL CHECK (attempt >= 1),
  idempotency_key char(64) NOT NULL,
  lease_token     bigint NOT NULL DEFAULT 0,
  worker          text,
  claimed_at      timestamptz,
  finished_at     timestamptz,
  outcome         text CHECK (outcome IN ('ok','fail','timeout','skipped')),
  CONSTRAINT recruit_job_key UNIQUE (idempotency_key),
  CONSTRAINT recruit_job_attempt_key UNIQUE (candidate_id, stage, attempt)
);

CREATE INDEX IF NOT EXISTS recruit_job_open
  ON recruit_jobs (candidate_id) WHERE finished_at IS NULL;

CREATE TABLE IF NOT EXISTS recruit_exclusions (
  kind     text NOT NULL CHECK (kind IN ('asn','prefix','ptr_regex','country','honeypot')),
  pattern  text NOT NULL,
  category text NOT NULL DEFAULT 'otro' CHECK (category IN (
             'banco','gobierno','sanidad','escaner','cert','propio','honeypot','otro')),
  note     text,
  active   boolean NOT NULL DEFAULT true,
  CONSTRAINT recruit_exclusion_key PRIMARY KEY (kind, pattern)
);

CREATE TABLE IF NOT EXISTS recruit_socks5_checks (
  check_id     bigserial PRIMARY KEY,
  candidate_id bigint NOT NULL REFERENCES recruit_candidates ON DELETE CASCADE,
  node_id      uuid NOT NULL REFERENCES nodes ON DELETE CASCADE,
  target       text NOT NULL,
  http_status  smallint,
  bytes_in     int NOT NULL DEFAULT 0,
  latency_ms   int,
  egress_ip    inet,
  ip_match     boolean NOT NULL DEFAULT false,
  ok           boolean NOT NULL DEFAULT false,
  attempt      smallint NOT NULL DEFAULT 1,
  checked_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT recruit_socks5_key UNIQUE (candidate_id, attempt),
  CONSTRAINT recruit_socks5_ok_evidence
    CHECK (ok = false OR (http_status = 200 AND bytes_in > 0
                          AND latency_ms BETWEEN 0 AND 1500 AND ip_match))
);

CREATE UNIQUE INDEX IF NOT EXISTS recruit_socks5_one_win
  ON recruit_socks5_checks (candidate_id) WHERE ok;

CREATE TABLE IF NOT EXISTS recruit_planner_ticks (
  tick_at       timestamptz PRIMARY KEY,
  rate_class    smallint NOT NULL,
  action        text NOT NULL CHECK (action IN ('up','down','hold','paused','blocked')),
  reason        text NOT NULL,
  pps_target    int,
  pps_effective int,
  probes_15m    int,
  host_fallback boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS recruit_waves (
  wave_no    smallint PRIMARY KEY,
  started_at timestamptz NOT NULL DEFAULT now(),
  closed_at  timestamptz,
  state      text NOT NULL DEFAULT 'abierta'
               CHECK (state IN ('abierta','cerrada','abortada')),
  promoted   int NOT NULL DEFAULT 0,
  enrolled   int NOT NULL DEFAULT 0,
  verified   int NOT NULL DEFAULT 0,
  note       text
);

CREATE TABLE IF NOT EXISTS recruit_source_ip (
  ip            inet PRIMARY KEY,
  ptr           text,
  abuse_contact text,
  rbl_listed    boolean NOT NULL DEFAULT false,
  rbl_checked_at timestamptz,
  paused_until  timestamptz,
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Semillas: singleton de control y politica horaria (BLUEPRINT §7:
-- 22-02 h clase 0, 02-06 h clase 3, 06-22 h clase 1).
INSERT INTO recruit_control (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

INSERT INTO recruit_window_policy (hour_utc, rate_class)
SELECT h, CASE WHEN h >= 22 OR h < 2 THEN 0 WHEN h >= 2 AND h < 6 THEN 3 ELSE 1 END
  FROM generate_series(0, 23) AS h
ON CONFLICT (hour_utc) DO NOTHING;
