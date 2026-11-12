# rdv por CNAME del cliente — destino compartible (T-1302, A1-01/A1-18)

**Que.** El destino de rendezvous del operador no es un dominio propio
exclusivo: cuelga como `CNAME` bajo un dominio del cliente (nube de CDN,
shop u OTA) que ya carga trafico ajeno. Para el DPI del enlace, el nodo
habla con una nube compartida, no con "la nube del operador".

**Registros.**

```text
rdv.example.            CNAME   rdv.client-cdn.example.   ; TTL 300
rdv.client-cdn.example. A       <gw clase-ASN-A>
rdv.client-cdn.example. A       <gw clase-ASN-B>          ; >= 2 ASNs (T-0407)
```

Un destino por ASN como minimo; el agente rota SNI/CNAME por nodo
(`internal/tunnel/dispersion.go`: `CNAMEForNode`, `ForNode`) y re-resuelve
con TTL + jitter (`resolver.go`).

**Verificacion.**

```bash
dig +short CNAME rdv.example        # -> el CNAME del cliente, no un nombre del operador
dig +short rdv.client-cdn.example   # -> >= 2 IPs en >= 2 ASNs
openssl s_client -connect <ip>:443 -alpn h2 -servername rdv.client-cdn.example </dev/null 2>/dev/null \
  | grep -i 'ALPN'                  # -> "ALPN protocol: h2" (TLS 1.3 sobre 443)
```

**Por que funciona (A1-01).** La firma "conexion larga unica hacia un AS
propio" exige que el destino no lleve trafico de nadie mas. Con CNAME
compartible, el prefijo destino ya lleva clientes reales del dueno del
dominio: el operador carga su trafico en espacio ajeno y el histograma de
destinos del nodo simulado deja de tener un unico pico.

**Cadencia en ventana (AC2).** Los frames de control completos viajan a
intervalo 60-150 s por nodo (ventanas, no constantes globales — exigencia
4 de A1): `ControlIntervals`/`WindowPassRate` en `internal/tunnel` lo
asertan con >= 95 % sobre captura simulada de 1 h.
