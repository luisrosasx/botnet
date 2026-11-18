// genticket firma un ticket de intrusion para pruebas de enrol.
//
// Ticket = v1.<sha256(token)>.<probe>.<iat>.<hmac> y el backend lo valida
// con proto.VerifyTicket usando RECRUIT_TICKET_SECRET. Imprime por stdout
// el ticket y, separado por salto de linea, el hash que va a `tickets`.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/botnet/botnet/pkg/proto"
)

func main() {
	sec := os.Getenv("RECRUIT_TICKET_SECRET")
	token := os.Getenv("TOKEN")
	probe := os.Getenv("PROBE")
	if sec == "" || token == "" {
		fmt.Fprintln(os.Stderr, "faltan RECRUIT_TICKET_SECRET y TOKEN")
		os.Exit(2)
	}
	if probe == "" {
		probe = "probe-1"
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	t := proto.Ticket{TokenHash: hash, ProbeID: probe, IAT: time.Now().Unix()}
	fmt.Println(proto.SignTicket([]byte(sec), t))
	fmt.Println(hash)
}
