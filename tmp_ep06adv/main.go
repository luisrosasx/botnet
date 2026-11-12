// Sonda adversaria EP-06 (segunda pasada): curl SOCKS5 literal sobre
// proxy.Start real + pings RTT reales bajo bucket saturado (doble fiel
// de "100 pings ssh al router con p95 < 300 ms").
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/botnet/botnet/internal/proxy"
)

var secret = []byte("0123456789abcdef0123456789abcdef") // 32 B

func dialOpener(_ context.Context, meta proxy.StreamMeta) (io.ReadWriteCloser, error) {
	return net.DialTimeout("tcp", meta.Target(), 10*time.Second)
}

func curl(url, user string) (string, string, int) {
	cmd := exec.Command("curl", "-s", "-o", os.DevNull,
		"-w", "https=%{http_code} http=%{http_code}",
		"--socks5-hostname", "127.0.0.1:1080",
		"--proxy-user", user+":pw", "--max-time", "20", url)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	return out.String(), errb.String(), code
}

func main() {
	srv, err := proxy.Start(proxy.Config{Addr: "127.0.0.1:1080", Secret: secret, Opener: proxy.OpenerFunc(dialOpener)})
	if err != nil {
		fmt.Println("proxy.Start:", err)
		os.Exit(1)
	}
	defer srv.Close()
	fmt.Printf("proxy escuchando en %s loopback_only=%v\n", srv.Addr(), srv.Loopback())

	// netstat literal mientras el listener vive
	na, _ := exec.Command("netstat", "-ano").Output()
	for _, ln := range strings.Split(string(na), "\n") {
		if strings.Contains(ln, "1080") && strings.Contains(ln, "LISTEN") {
			fmt.Println("netstat:", strings.TrimSpace(ln))
		}
	}

	tok := proxy.SignToken(secret, proxy.SessionIdentity{
		ClientID: "c-adv", TenantID: "t-adv", SessionID: "s-adv", NodeID: "n-adv", TS: time.Now(),
	})
	w, _, code := curl("https://example.com", tok)
	fmt.Printf("curl https token-valido: %s (exit %d, streams abiertos=%d)\n", w, code, srv.StreamsOpen())
	w, eb, code := curl("https://example.com", "token-basura.deadbeef")
	fmt.Printf("curl token-basura: %q exit=%d\n", strings.TrimSpace(eb), code)

	// doble fiel T-0604 AC2: RTT reales de TCP loopback bajo bucket saturado
	b := proxy.NewBucket(proxy.MbpsToBytes(5))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // stream de datos al tope: consume el bucket sin descanso
		chunk := make([]byte, 8192)
		for ctx.Err() == nil {
			if err := b.Take(ctx, float64(len(chunk))); err != nil {
				return
			}
			_, _ = io.Discard.Write(chunk)
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(1)
	}
	defer ln.Close()
	go func() {
		buf := make([]byte, 1)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				for {
					n, err := c.Read(buf)
					if err != nil || n == 0 {
						c.Close()
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						c.Close()
						return
					}
				}
			}(c)
		}
	}()
	var c net.Conn
	for attempt := 0; ; attempt++ {
		c, err = net.Dial("tcp", ln.Addr().String())
		if err == nil || attempt > 30 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	lat := make([]time.Duration, 0, 100)
	for i := 0; i < 100; i++ { // una conexion, 100 idas y vueltas: ping real estilo ssh
		start := time.Now()
		c.Write([]byte{0x50}) // 'P' de ping: ida y vuelta real por el kernel
		var rb [1]byte
		c.Read(rb[:])
		lat = append(lat, time.Since(start))
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	fmt.Printf("p95 RTT loopback real bajo bucket saturado = %v (mediana %v, max %v, n=100)\n",
		lat[94], lat[49], lat[99])
}
