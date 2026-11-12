// probe — binario trivial para medir la línea base del emulador.
//
// Bajo qemu-user, /proc del proceso emulado es el del propio qemu, así que
// el RSS del agente no se puede leer: se mide el pico del proceso con el
// agente y se le descuenta el pico del mismo emulador arrancando esto. El
// resultado es una cota del footprint del agente, no su cifra exacta (esa
// exige hardware real).
package main

import "fmt"

func main() {
	fmt.Println("probe ok")
}
