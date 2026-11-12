# BRIEF BASE — BOT.NET (Propuesta B)

Documento de contexto compartido. **Todo subagente del proceso lee esto antes de trabajar.**

## Objetivo del proyecto

`BOT.NET` es una red de proxies residenciales construida sobre dispositivos abiertos de internet
(routers, cámaras IP, NAS, grabadores) con credenciales por defecto o firmware vulnerable, revendida
por GB con modelo **backconnect**: el cliente se conecta a un endpoint único y el sistema elige el
dispositivo de salida; el cliente nunca ve la IP del bot.

## Propuesta B (base inmutable del diseño)

| Capa | Componente |
|---|---|
| Descubrimiento | zgrab2 + Shodan API; masscan para barrido propio |
| Intrusión | legba (credenciales multi-protocolo) + RouterSploit; lista de CVEs por modelo en un YAML |
| Entrega | sliver-builder automatizado: perfiles por arquitectura, stagers con reintentos y jitter |
| Implante | Extensión Sliver en Go que gestiona el ciclo del proxy y reporta al backend |
| Proxy | gost v3 embebido como librería: SOCKS5 con auth, rotación y multiplexado |
| Control | Sliver server (mTLS + WireGuard); headscale/WireGuard como malla privada por nodo |
| Persistencia | Servicio systemd / rc.local / cron según capacidad del dispositivo |
| Backend | Go (Fiber) + Postgres + Redis + NATS para eventos de nodo; GeoIP2 + ASN |
| Gateway backconnect | Traefik + gost con selección por país/ASN/ciudad desde Redis |
| Panel | Next.js, Stripe, cuotas y rate-limit en Redis, auditoría de sesiones |

## Restricciones físicas y de entorno (no negociables)

1. **CGNAT**: la mayoría de dispositivos no tiene IP entrante. El dispositivo **siempre inicia** la
   conexión hacia la infraestructura. Nada de clientes que conecten de ida al bot.
2. **Sin root garantizado**: la intrusión típica es credencial de admin del panel web + command
   injection, o Telnet/SSH. El agente debe funcionar **como usuario sin privilegios**; todo lo que
   exija root es opcional y best-effort.
3. **Arquitecturas objetivo**: MIPS-BE, MIPS-LE, ARMv5/7, ARM64, x86-64. Todo binario estático
   (`CGO_ENABLED=0`, musl). Un binario mal compilado pierde la familia entera.
4. **Hardware pobre**: routers de 32 MB de RAM y 8 MB de flash. Presupuesto de RSS del agente
   ≤ 6 MB. Sin compilación con `-race`.
5. **Persistencia frágil**: los dispositivos reinician solos y el usuario puede apagarlos. Sin
   systemd, hace falta doble proceso watchdog.
6. **Ancho de banda ajeno**: límite por nodo de 2-5 Mbps o el dueño nota el router lento.
7. **Legal**: el dispositivo no es nuestro. El binario deja endpoint de transparencia y de baja.

## Decisiones de negocio ya tomadas

- Modelo **multi-tenant** (varios clientes simultáneos), con cuotas por GB y por sesiones concurrentes.
- Selección de exit por **país / ASN / ciudad / tipo de dispositivo**, con modo *sticky* (misma IP
  por un TTL) y *rotativo*.
- Facturación por GB consumido, con corte de sesión al agotar cuota.
- Contabilidad por sesión y por nodo desde el día uno.

## Convenciones de los documentos

- Prosa en **español**; identificadores, rutas, nombres de tablas, columnas, endpoints, variables de
  entorno y campos de API en **inglés**.
- Cada artefacto abre con un front-matter YAML: `artifact`, `wave`, `author_role`, `created`.
- Nada de código de producto (solo firmas, esquemas, pseudofirmas y árboles de ficheros).
- Toda afirmación técnica lleva su motivo. Toda decisión lleva su alternativa descartada.

## Definición de "listo para ejecución"

Un backlog sirve si un agente implementador, sin contexto adicional, puede tomar una TASK y
completarla: alcance delimitado, ficheros a tocar, criterios de aceptación ejecutables
(comando + salida esperada, no adjetivos), dependencias resueltas y definición de hecho.

## Directorios de trabajo

- `docs/blueprint/w1-proposals/` — propuestas rivales
- `docs/blueprint/w2-defense/` — endurecimiento por propuesta
- `docs/blueprint/w3-attack/` — ataques adversarios
- `docs/blueprint/w4-verdicts/` — matrices y veredictos
- `docs/blueprint/BLUEPRINT.md` — documento final
- `docs/blueprint/BACKLOG.md`, `EPICS/`, `TASKS/` — plan ejecutable
