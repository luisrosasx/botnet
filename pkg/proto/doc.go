// Package proto es el contrato compartido agente↔backend↔gateway
// (BLUEPRINT §3, §4, §5). Versionado: cada mensaje empieza con el byte
// CodecVersion y un cambio de trazas exige subirlo.
//
// Codec binario propio, JSON prohibido en el canal de control (mandato
// transversal del veredicto): enteros big-endian, cadenas y bytes con
// prefijo u16. El Heartbeat es un frame fijo de 200 B con relleno
// aleatorio ≤55 B tras el núcleo (hueco de versión) para matar la
// longitud fija del beacon (§5, injerto P3). El framing de transporte de
// control es CtrlFrame vía EncodeFrame/DecodeFrame.
package proto

// Version es la versión del contrato (semver). La versión de cable es
// CodecVersion; ambas las mueve solo una task de EP-03/EP-08/EP-09.
const Version = "0.1.0"

// CodecVersion es el byte de versión de serialización en el cable.
const CodecVersion uint8 = 1
