-- Fixture de 100 000 filas para el EXPLAIN de T-0106.3: IPs /24
-- densas (el dedupe por /24 del informe es visible) y todas con
-- service NOT NULL para que el indice parcial sea util.
INSERT INTO discovered (ip, port, service, asn, country, device_class_guess, source, last_seen_at)
SELECT ('45.' || ((i / 65536) % 250)
           || '.' || ((i / 256) % 250)
           || '.' || (i % 250))::inet,
       80,
       'http',
       (ARRAY[4134, 9829, 7922, 6181, 3352, 3320, 3329, 2856, 9009, 45609])[1 + (i % 10)],
       'US',
       'router',
       'masscan',
       now() - ((i % 25) || ' days')::interval
FROM generate_series(1, 100000) AS i
ON CONFLICT (ip, port) DO NOTHING;
ANALYZE discovered;
