-- EP-11 T-1103: borrado RGPD con retencion contable (A3-X5). Las
-- columnas ya vivian en 0003 (deleted_at, mac_hash, device_token_hash,
-- public_ip); aqui entra el escaneo del job purge, que pregunta por
-- leaves.left_at > 13 meses para anular identidad sin borrar factura.
CREATE INDEX IF NOT EXISTS leaves_left_at_idx ON leaves (left_at);
