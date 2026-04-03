package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// selfSignedTLSConfig returns a *tls.Config backed by a self-signed ECDSA
// certificate.  The cert/key PEM files are persisted to certDir so that the
// browser only needs to accept the cert once (the same cert is reused on
// subsequent starts).
func selfSignedTLSConfig(certDir string) (*tls.Config, error) {
	certPath := filepath.Join(certDir, "ubersdr_cert.pem")
	keyPath := filepath.Join(certDir, "ubersdr_key.pem")

	// Try to load an existing cert/key pair first.
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
	}

	// Generate a new ECDSA P-256 key.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	// Self-signed cert valid for 10 years.
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"ubersdr_qsstv"},
			CommonName:   "ubersdr_qsstv",
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		// Include common LAN addresses so the cert is valid for typical deployments.
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
		DNSNames: []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	// Write cert PEM.
	cf, err := os.Create(certPath)
	if err != nil {
		return nil, err
	}
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		cf.Close()
		return nil, err
	}
	cf.Close()

	// Write key PEM.
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	kf, err := os.Create(keyPath)
	if err != nil {
		return nil, err
	}
	if err := pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}); err != nil {
		kf.Close()
		return nil, err
	}
	kf.Close()

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}
