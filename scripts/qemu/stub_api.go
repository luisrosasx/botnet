// stub_api — stub del plano de control para el humo QEMU de T-1402.
// Responde el 201 del enroll con las claves del contrato (BLUEPRINT §4:
// {node_id, slug, cert_pem, ca_pem, node_secret, rendezvous[], ttl_s}) y
// 200 "ok" al heartbeat. No persiste nada: el humo mide arranque, enroll y
// latido, no backend.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address (:0 = puerto libre)")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		// Las claves del contrato viajan siempre; el humo aserta su presencia.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"node_id":"11111111-2222-3333-4444-555555555555",`+
			`"slug":"smokeslug1234","cert_pem":"-----BEGIN CERT-----stub",`+
			`"ca_pem":"-----BEGIN CERT-----ca","node_secret":"`+
			"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e0f10"+
			`","rendezvous":["rdv1.botnet.example:443","rdv2.botnet.example:443"],`+
			`"ttl_s":120}`)
		log.Printf("ENROLL 201 %s %s", r.Method, r.URL.Path)
	})
	mux.HandleFunc("/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
		log.Printf("HEARTBEAT 200 ok")
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})

	// El host puede tener otros servicios en el puerto elegido: se hace
	// bind explicito y se publica la direccion REAL para que el llamante
	// la lea del log en vez de adivinarla.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("stub_api bind %s: %v", *addr, err)
	}
	log.Printf("stub_api listening on %s", ln.Addr().String())
	if err := http.Serve(ln, mux); err != nil {
		log.Fatal(err)
	}
}
