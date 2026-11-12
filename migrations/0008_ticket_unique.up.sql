-- EP-03 T-0305 AC2/AC3: la unicidad del ticket de intrusion pasa al
-- modelo de datos. Antes vivia solo en Redis (SETNX ticket:{tokenHash},
-- TTL 90 d), asi que el reparto concurrente real salia 1x201 + Nx403 y
-- un FLUSHDB resucitaba tickets ya consumidos. El alta estampa aqui el
-- sha256 hex del ticket wire consumido y el indice unico parcial (solo
-- filas vivas, NULL ignorado) pone la ganadora unica a nivel de fila:
-- el conflicto de indice es el 409 del contrato.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS ticket_hash char(64);

CREATE UNIQUE INDEX IF NOT EXISTS nodes_ticket_hash_live_uniq
    ON nodes (ticket_hash)
 WHERE deleted_at IS NULL AND ticket_hash IS NOT NULL;
