# Registro inverso del origen (PTR) y contacto de abuso

El barrido sale por una única IPv4, `74.208.94.91`. Un origen sin nombre se
confunde con cualquier escáner anónimo y es lo primero que se bloquea en las
listas de abuso. Este documento fija el registro que hay que tener y el
contacto que se publica con él.

## Medido antes de la épica

```
$ dig -x 74.208.94.91 +short
ubuntu.
ubuntu.local.
```

Es el PTR genérico de la plantilla del proveedor: no dice qué servicio hay
detrás ni a quién se escribe si molesta.

## Registro que hay que crear

| Tipo | Nombre | Valor |
| --- | --- | --- |
| PTR (zona del proveedor) | `91.94.208.74.in-addr.arpa` | `recruit.botnet.example.` |
| A (zona propia) | `recruit.botnet.example` | `74.208.94.91` |
| TXT (zona propia) | `_recruit.botnet.example` | `v=botnet-origen; rol=barrido; pps_max=5000` |

## Contacto de abuso

`abuse@botnet.example` como buzón real con entrega comprobada (un mensaje de
prueba, cero rebotes). El servicio lo escribe en `recruit_source_ip.abuse_contact`
y es lo que se declara si alguien pregunta por el tráfico.

```bash
set -a; . /etc/botnet-recruit/env; set +a
psql -tAc "SELECT ip, ptr, abuse_contact FROM recruit_source_ip" "$DATABASE_URL"
```

## Estado

- [x] PTR medido y anotado (`ubuntu.`).
- [ ] PTR propio: depende del panel del proveedor del VPS, no se puede poner
      desde dentro de la máquina.
- [ ] `abuse@` propio con entrega verificada.

Mientras el PTR siga siendo el genérico, la comprobación de reputación
(`scripts/recruit/rbl_probe.sh`) sigue siendo la que avisa: si el origen entra
en lista negra, el reclutamiento se pausa 48 horas.
