// Package intrusion es la lógica de EP-02 (INTRUSIÓN Y ENTREGA):
//   - lista de CVEs por modelo como dato versionado con esquema validado (cve.go),
//   - veredicto del runner legba multi-protocolo con salida JSON (legba.go),
//   - política del stager de entrega: destino por clase de flash, reintentos con
//     jitter y tope del spool contable en tmpfs (stager.go).
//
// Los runners en shell (scripts/intrusion/*.sh, deploy/sliver-builder/*.sh)
// son envoltorios finos: la decisión vive aquí, medible por tests. Hardware
// real (QEMU, tc netem, upx) se prueba con dobles y fixtures; la limitación
// se anota en docs/runbooks/intrusion.md.
package intrusion
