// Command apistub es el doble del backend F1 para los soak del watchdog:
// responde /v1/enroll (201) y /v1/heartbeat (200) sin persistencia. Con
// --addr 127.0.0.1:0 imprime el puerto real por stdout y el script lo
// captura. Sin root, sin dependencias: stdlib.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address (0 = puerto libre)")
	flag.Parse()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"node_id":"11111111-1111-2222-3333-444444444444","slug":"soaksoak0001","cert_pem":"x","key_pem":"y","ca_pem":"z","endpoint":"https://rdv.botnet.example:443","ttl_s":900,"renew_after_s":60}`)
	})
	http.HandleFunc("/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	fmt.Printf("%d\n", ln.Addr().(*net.TCPAddr).Port)
	log.Fatal(http.Serve(ln, nil))
}
