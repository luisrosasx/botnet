-- 0006_geo_verified.up.sql — EP-07 T-0704 (geo del exit sondeada,
-- A3-X4) y T-0706 (codigo de baja con puerta legal, A3-13).
-- La geo autorreportada deja de ser la puerta del pool: la sonda
-- escribe pais/ASN observados y geo_verified manda; el codigo de 8
-- caracteres vive en nodes hasta que el panel lo gire.

ALTER TABLE nodes ADD COLUMN country_probed char(2);
ALTER TABLE nodes ADD COLUMN asn_probed int;
ALTER TABLE nodes ADD COLUMN geo_checked_at timestamptz;
ALTER TABLE nodes ADD COLUMN leave_code char(8);

-- Historia de sondas: una fila por sonda semanal (7 por nodo cada 7
-- dias, AC2) con el presupuesto de bytes tx de la propia sonda.
CREATE TABLE geo_probes (
  probe_id bigserial PRIMARY KEY,
  node_id uuid NOT NULL REFERENCES nodes,
  checked_at timestamptz NOT NULL DEFAULT now(),
  country char(2) NOT NULL,
  asn int NOT NULL,
  city text,
  verified boolean NOT NULL,
  bytes_tx int NOT NULL DEFAULT 0 CHECK (bytes_tx >= 0),
  method text NOT NULL DEFAULT 'mesh_exit'
);

CREATE INDEX geo_probes_node_time ON geo_probes (node_id, checked_at);

-- Pool «residencial»: la seleccion lee geo_verified + country_probed;
  -- el indice parcial es exactamente esa puerta (AC3).
CREATE INDEX nodes_residencial_pool ON nodes (country_probed)
    WHERE geo_verified;
