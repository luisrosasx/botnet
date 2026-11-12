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
	secret, _ := hex.DecodeString(os.Args[1])
	sum := sha256.Sum256([]byte(os.Args[2]))
	fmt.Print(proto.SignTicket(secret, proto.Ticket{TokenHash: hex.EncodeToString(sum[:]), ProbeID: "probe-live", IAT: time.Now().Unix()}))
}
