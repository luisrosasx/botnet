// Package services: logica de negocio del plano de control (capa entre
// handlers y repo, BLUEPRINT §9). La CA de flota es offline: su clave
// privada vive en el signer (deploy/signing/ca_policy.md), nunca en el
// path de un request de panel.
package services

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

// FleetCN es la unica CN admitida por la politica de flota: un CSR con
// CN ajena (la flota no es un CA/WebPKI publico) responde 422.
const FleetCN = "botnet-node"

// CertTTL: certificado de cliente a 90 dias (§6); el node_secret rota
// con el (A3-06), asi que ambos comparten TTL.
const CertTTL = 90 * 24 * time.Hour

var (
	// ErrCSRForged: la autocfirma del CSR no calza (o es ilegible) -> 400.
	ErrCSRForged = errors.New("services: CSR con firma forjada o ilegible")
	// ErrCNPolicy: CN fuera de la politica de flota -> 422.
	ErrCNPolicy = errors.New("services: CN fuera de politica de flota")
	// ErrKeyWeak: RSA < 2048 o algoritmo no soportado -> 422.
	ErrKeyWeak = errors.New("services: clave publica debil o no soportada")
)

// CA es la CA offline de flota cargada en el signer.
type CA struct {
	cert    *x509.Certificate
	key     crypto.Signer
	CertPEM []byte
}

// LoadCA lee cert+clave PEM del signer (env CA_CERT_PEM/CA_KEY_PEM).
func LoadCA(certPath, keyPath string) (*CA, error) {
	certRaw, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("services: CA cert: %w", err)
	}
	keyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("services: CA key: %w", err)
	}
	return LoadCAFromPEM(certRaw, keyRaw)
}

// LoadCAFromPEM parsea un par CA; la clave acepta PKCS#8 ECDSA/RSA.
func LoadCAFromPEM(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, errors.New("services: CA cert PEM ilegible")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("services: CA cert: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, errors.New("services: CA key PEM ilegible")
	}
	key, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("services: CA key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("services: CA key no es signer")
	}
	return &CA{cert: cert, key: signer, CertPEM: certPEM}, nil
}

// NewCA genera la CA de desarrollo/tests (P-256, 10 anos). En produccion
// la CA nace en la ceremonia offline y se instala en el signer.
func NewCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("services: CA keygen: %w", err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "BOT.NET fleet CA", Organization: []string{"BOT.NET"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("services: CA selfsign: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("services: CA parse: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return &CA{cert: cert, key: key, CertPEM: pemBytes}, nil
}

// SignCSR firma el CSR del enroll y emite el certificado de cliente con
// TTL de 90 dias. Devuelve el PEM del cert, el serial en hex (clave de
// la CRL `revoked:{serial}`) y el notAfter efectivo.
func (c *CA) SignCSR(csrPEM []byte, now time.Time) (certPEM []byte, serialHex string, notAfter time.Time, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, "", time.Time{}, ErrCSRForged
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", time.Time{}, ErrCSRForged
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", time.Time{}, ErrCSRForged
	}
	if csr.Subject.CommonName != FleetCN {
		return nil, "", time.Time{}, ErrCNPolicy
	}
	if err := checkPubKey(csr.PublicKey); err != nil {
		return nil, "", time.Time{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 152))
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("services: serial: %w", err)
	}
	notAfter = now.Add(CertTTL)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: FleetCN, Organization: []string{"BOT.NET"}},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, csr.PublicKey, c.key)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("services: sign csr: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return pemBytes, hex.EncodeToString(serial.Bytes()), notAfter, nil
}

// checkPubKey: ECDSA P-256/384 o RSA >= 2048; lo demas (incluido DSA
// de 2009) queda fuera de la politica de flota.
func checkPubKey(pub crypto.PublicKey) error {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256(), elliptic.P384():
			return nil
		}
	case *rsa.PublicKey:
		if k.N.BitLen() >= 2048 {
			return nil
		}
	}
	return ErrKeyWeak
}
