// gentoken emite un bot-session firmado para pruebas de la verificacion
// SOCKS5: imprime el token y el secreto en hex, separados por salto de linea.
package main

import (
	"fmt"
	"time"

	"github.com/botnet/botnet/internal/proxy"
)

func main() {
	sec := make([]byte, 32)
	for i := range sec {
		sec[i] = 0xab
	}
	id := proxy.SessionIdentity{ClientID: "c", TenantID: "t", SessionID: "s", NodeID: "11111111-1111-1111-1111-111111111111", TS: time.Now().UTC()}
	fmt.Printf("%s\n%s\n", proxy.SignToken(sec, id), fmt.Sprintf("%x", sec))
}
