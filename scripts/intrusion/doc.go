// Package intrusion ancla scripts/intrusion como paquete Go para que
// `go test ./scripts/intrusion/` ejecute la validación de esquema del YAML
// de CVEs (T-0203) sin depender de un binario; la lógica vive en
// internal/intrusion. Los runners shell conviven en este directorio.
package intrusion
