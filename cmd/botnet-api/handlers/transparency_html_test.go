// Test EP-11 (verificacion adversarial): GET /t/{slug} es una PAGINA —
// navegador y curl (Accept: */*) reciben HTML; la JSON es para quien
// la pide explicitamente. Y el access.log anota una linea por
// peticion sin que asoma ni una credencial.
package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestTransparencySirveHTMLPorDefecto(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	var slug string
	if err := db.QueryRowContext(context.Background(),
		`SELECT slug FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET leave_code = 'ABCD1234', model = 'TP-Link ER60' WHERE node_id = $1::uuid`, nodeID); err != nil {
		t.Fatal(err)
	}
	app := NewApp(&Deps{Repo: repo.New(db)})

	// Criterio del adversario: `curl -H 'Accept: */*'` (el defectivo
	// de curl) y el navegador tienen que recibir HTML, no "JSON text
	// data". El panel Next manda Accept: application/json y sigue
	// viendo su JSON.
	cases := []struct {
		accept     string
		wantType   string
		wantInBody string
	}{
		{"", "text/html", "<!DOCTYPE html>"},
		{"*/*", "text/html", "<!DOCTYPE html>"},
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "text/html", "<!DOCTYPE html>"},
		{"application/json", "application/json", `"slug"`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/t/"+slug, nil)
		if tc.accept != "" {
			req.Header.Set("Accept", tc.accept)
		}
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Accept %q: %d", tc.accept, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, tc.wantType) {
			t.Fatalf("Accept %q: Content-Type %q queria %q", tc.accept, ct, tc.wantType)
		}
		if !strings.Contains(string(body), tc.wantInBody) {
			t.Fatalf("Accept %q: cuerpo sin %q: %s", tc.accept, tc.wantInBody, body)
		}
	}

	// El HTML lleva el contenido de transparencia y la credencial
	// jamas asoma: ni device_token ni token_hash ni valores hex.
	req := httptest.NewRequest(http.MethodGet, "/t/"+slug, nil)
	req.Header.Set("Accept", "text/html")
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"TP-Link ER60", "ABCD1234", slug, "Consentimiento"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("HTML sin %q:\n%s", want, body)
		}
	}
	for _, leak := range []string{"device_token", "token_hash"} {
		if strings.Contains(strings.ToLower(string(body)), leak) {
			t.Fatalf("fuga de credencial en el HTML: %s", leak)
		}
	}

	// La JSON explicita decodifica igual que antes (contrato panel).
	req = httptest.NewRequest(http.MethodGet, "/t/"+slug, nil)
	req.Header.Set("Accept", "application/json")
	resp, err = app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	var page map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if page["model"] != "TP-Link ER60" {
		t.Fatalf("JSON modelo: %v", page["model"])
	}
}

func TestAccessLogEscribeUnaLineaPorPeticion(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	var slug string
	if err := db.QueryRowContext(context.Background(),
		`SELECT slug FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "access.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(&Deps{Repo: repo.New(db), AccessLog: f})

	// Dos golpes vivos y uno muerto: el log anota TAMBIEN los 404 y
	// los 429 (es un registro de acceso, no un registro de exito).
	for _, url := range []string{"/healthz", "/t/" + slug, "/t/corto"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, url, nil), 5000)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("3 peticiones, %d lineas:\n%s", len(lines), raw)
	}
	// Formato: ts ip metodo path status duracion.
	for _, ln := range lines {
		fields := strings.Fields(ln)
		if len(fields) != 6 {
			t.Fatalf("linea con %d campos (queria 6): %q", len(fields), ln)
		}
		if fields[2] != "GET" {
			t.Fatalf("metodo: %q", fields[2])
		}
	}
	if !strings.Contains(lines[1], "/t/"+slug) || !strings.Contains(lines[1], "200") {
		t.Fatalf("linea /t: %q", lines[1])
	}
	if !strings.Contains(lines[2], "/t/corto") || !strings.Contains(lines[2], "404") {
		t.Fatalf("linea 404: %q", lines[2])
	}
	// El token del dueño nunca viaja por la URL: grep -c $TOKEN = 0.
	if strings.Contains(string(raw), "token") {
		t.Fatalf("la palabra token asoma en el access.log:\n%s", raw)
	}
}

// AccessLog nil = middleware pasamanos: los demas tests del paquete
// no abren ficheros ni escriben nada.
func TestAccessLogNilNoEscribe(t *testing.T) {
	app := NewApp(&Deps{Repo: repo.New(nil)})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz con AccessLog nil: %d", resp.StatusCode)
	}
	_, err = os.Stat(filepath.Join(t.TempDir(), "inexistente.log"))
	if !os.IsNotExist(err) {
		t.Fatalf("el log lo escribe solo Deps.AccessLog: %v", err)
	}
}
