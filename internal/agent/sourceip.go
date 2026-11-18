package agent

import (
	"net"
	"net/http"
	"time"
)

// SourceIPClient es el http.Client del agente cuando el despliegue fija la
// IP con la que el nodo sale a la red. Nace de -source-ip y se usa en el
// alta y en el latido, de forma que el plano de control anota esa IP como
// nodes.public_ip (A1-04: c.IP() en el enroll y en MarkLive).
//
// Es el mecanismo de la flota de laboratorio de T-2102: veinte agentes en
// una sola maquina, cada uno con su 127.0.0.x propia, sin enganar al
// backend — el socket del agente nace atado a su direccion.
//
// sourceIP vacio o invalido devuelve el cliente historico (15 s de timeout
// y la IP que elija el kernel).
func SourceIPClient(sourceIP string) *http.Client {
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return &http.Client{Timeout: 15 * time.Second}
	}
	d := net.Dialer{
		Timeout:   10 * time.Second,
		LocalAddr: &net.TCPAddr{IP: ip},
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: d.DialContext},
	}
}
