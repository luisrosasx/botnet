package agent

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// ParseUUID acepta la forma canónica 8-4-4-4-12 sin dependencia nueva:
// el backend devuelve node_id como string y el cable usa [16]byte.
func ParseUUID(s string) ([16]byte, error) {
	var id [16]byte
	clean := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	raw, err := hex.DecodeString(clean)
	if err != nil || len(raw) != 16 {
		return id, fmt.Errorf("agent: node_id %q no es uuid canónico", s)
	}
	copy(id[:], raw)
	return id, nil
}

// FormatUUID devuelve la forma canónica para headers y node.json.
func FormatUUID(id [16]byte) string {
	h := hex.EncodeToString(id[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
