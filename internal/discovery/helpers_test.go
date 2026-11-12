package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// openTestdata abre un fixture desde el raiz del repo, independiente
// del cwd de `go test`.
func openTestdata(t *testing.T, rel string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("fixture %s: %v", rel, err)
	}
	return f
}
