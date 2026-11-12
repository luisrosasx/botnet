-- EP-08 T-0801: DDL del nucleo contable e inventario del blueprint §9.
-- El suelo reservo 0001_init (placeholder) y 0002_discovered (EP-01),
-- asi que el core entra como 0003 y los indices como 0004. Solo adelante,
-- sin ORM, SQL plano (BLUEPRINT §9).

CREATE TABLE tenants (
  tenant_id uuid PRIMARY KEY, name text NOT NULL, plan text NOT NULL DEFAULT 'trial',
  stripe_customer_id text UNIQUE, stripe_price_id text,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','closed')),
  created_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz);

CREATE TABLE api_keys (
  api_key_id bigserial PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants,
  key_hash char(64) NOT NULL UNIQUE, label text,
  mode text NOT NULL DEFAULT 'rotating' CHECK (mode IN ('sticky','rotating')),
  sticky_ttl_s int NOT NULL DEFAULT 300 CHECK (sticky_ttl_s BETWEEN 30 AND 3600),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
  created_at timestamptz NOT NULL DEFAULT now(), last_used_at timestamptz);

CREATE TABLE nodes (
  node_id uuid PRIMARY KEY, slug char(12) NOT NULL UNIQUE,
  device_token_hash char(64) NOT NULL UNIQUE,
  arch text NOT NULL CHECK (arch IN ('mipsbe','mipsle','armv5','armv7','arm64','amd64')),
  device_class text NOT NULL CHECK (device_class IN ('A','B','C')),
  model text, firmware text, mac_hash bytea,
  public_ip inet, country char(2), asn int, city text,
  geo_verified boolean NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'enrolled'
    CHECK (status IN ('enrolled','live','down','retiring','retired','burned')),
  agent_version text, stable_version text,
  bandwidth_cap_kbps smallint NOT NULL DEFAULT 2048,
  monthly_gb_cap smallint NOT NULL DEFAULT 150,
  consent_at timestamptz NOT NULL, optout_at timestamptz, -- [injerto: P3] suelo legal
  enrolled_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz, deleted_at timestamptz);

CREATE TABLE node_secrets ( -- [injerto: P2] cierra el S1 A3-06
  secret_id bigserial PRIMARY KEY, node_id uuid NOT NULL REFERENCES nodes,
  secret bytea NOT NULL CHECK (octet_length(secret) = 32),
  issued_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
  revoked_at timestamptz);

CREATE TABLE sessions (
  session_id bigserial PRIMARY KEY,
  node_id uuid NOT NULL REFERENCES nodes, tenant_id uuid NOT NULL REFERENCES tenants,
  api_key_id bigint REFERENCES api_keys, client_ref text, gw_id text NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz,
  bytes_in bigint NOT NULL DEFAULT 0 CHECK (bytes_in >= 0),
  bytes_out bigint NOT NULL DEFAULT 0 CHECK (bytes_out >= 0),
  closed_reason text CHECK (closed_reason IN ('quota','error','client','goaway','drain','purge')));

CREATE TABLE usage ( -- medidor autoritativo del nodo; retencion 12 meses
  day date NOT NULL, node_id uuid NOT NULL REFERENCES nodes,
  bytes bigint NOT NULL CHECK (bytes >= 0),
  cost_minor int, currency char(3) NOT NULL DEFAULT 'USD',
  reconciled boolean NOT NULL DEFAULT false,
  PRIMARY KEY (day, node_id));

CREATE TABLE quotas (
  quota_id bigserial PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants,
  gb_allowance numeric(10,2) NOT NULL CHECK (gb_allowance > 0),
  concurrent_limit smallint NOT NULL DEFAULT 50 CHECK (concurrent_limit BETWEEN 1 AND 500),
  mode text NOT NULL DEFAULT 'rotating' CHECK (mode IN ('sticky','rotating')),
  period_start date NOT NULL, UNIQUE (tenant_id, period_start));

CREATE TABLE leaves (
  leave_id bigserial PRIMARY KEY, node_id uuid NOT NULL REFERENCES nodes,
  method text NOT NULL CHECK (method IN ('http','heartbeat_bye','panel','owner_url')),
  reason text, leave_code char(8),
  left_at timestamptz NOT NULL DEFAULT now(),
  reprobe_blocked_until timestamptz NOT NULL); -- re-clutaje bloqueado >=12 meses
