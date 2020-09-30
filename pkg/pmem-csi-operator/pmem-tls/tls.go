/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemtls

import (
	"crypto"
	"errors"
	"math"
	"runtime"
	"time"

	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
)

const (
	rasKeySize = 3072
)

// NewPrivateKey generate an rsa private key
func NewPrivateKey() (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, rasKeySize)
	if err != nil {
		return nil, err
	}

	runtime.SetFinalizer(key, func(k *rsa.PrivateKey) {
		// Zero key after usage
		*k = rsa.PrivateKey{}
	})
	return key, nil
}

// EncodeKey returns PEM encoding of give private key
func EncodeKey(key *rsa.PrivateKey) []byte {
	if key == nil {
		return []byte{}
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

// DecodeKey returns the decoded private key of given encodedKey
func DecodeKey(encodedKey []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(encodedKey)

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	wipe(block.Bytes)
	if err != nil {
		return nil, err
	}

	runtime.SetFinalizer(key, func(k *rsa.PrivateKey) {
		// Zero key after usage
		*k = rsa.PrivateKey{}
	})

	return key, nil
}

// EncodeCert returns PEM encoding of given cert
func EncodeCert(cert *x509.Certificate) []byte {
	if cert == nil {
		return []byte{}
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})
}

// DecodeCert return the decoded certificate of given encodedCert
func DecodeCert(encodedCert []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(encodedCert)

	cert, err := x509.ParseCertificate(block.Bytes)
	wipe(block.Bytes)

	if err != nil {
		return nil, err
	}
	runtime.SetFinalizer(cert, func(c *x509.Certificate) {
		*c = x509.Certificate{}
	})

	return cert, nil
}

func wipe(arr []byte) {
	for i := range arr {
		arr[i] = 0
	}
}

// CA type representation for a self-signed certificate authority
type CA struct {
	prKey *rsa.PrivateKey
	cert  *x509.Certificate
}

// NewCA creates a new CA object for given CA certificate and private key.
// If both of caCert and key are nil, generates a new private key and
// a self-signed certificate
func NewCA(caCert *x509.Certificate, key *rsa.PrivateKey) (*CA, error) {
	var err error
	prKey := key
	cert := caCert
	if cert == nil {
		if prKey == nil {
			prKey, err = NewPrivateKey()
			if err != nil {
				return nil, err
			}
		}

		cert, err = NewCACertificate(prKey)
		if err != nil {
			return nil, err
		}
	} else if prKey == nil {
		return nil, errors.New("certificate is provided but not the associated private key is missing")
	} else {
		requiredKeyUsages := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
		if cert.KeyUsage&requiredKeyUsages != requiredKeyUsages {
			return nil, errors.New("provided certificates can not be used as CA certificate as" +
				" is not usable for encrypting or signing other keys")
		}

		if cert.IsCA != true {
			return nil, errors.New("provided certificate is not a ca certificate")
		}
	}

	ca := &CA{
		prKey: prKey,
		cert:  cert,
	}
	return ca, nil
}

// PrivateKey returns private key used
func (ca *CA) PrivateKey() *rsa.PrivateKey {
	return ca.prKey
}

// Certificate returns root ca certificate used
func (ca *CA) Certificate() *x509.Certificate {
	return ca.cert
}

// EncodedKey returns encoded private key used
func (ca *CA) EncodedKey() []byte {
	return EncodeKey(ca.prKey)
}

// EncodedCertificate returns encoded root ca certificate used
func (ca *CA) EncodedCertificate() []byte {
	return EncodeCert(ca.cert)
}

// GenerateCertificate returns a new certificate signed for given public key.
func (ca *CA) GenerateCertificate(cn string, key crypto.PublicKey) (*x509.Certificate, error) {
	return ca.generateCertificate(cn, ca.cert.NotBefore, time.Now().Add(time.Hour*24*365), key)
}

// GenerateCertificateWithDuration returns a new certificate signed for given public key.
// The duration of this certificate is with in the given notBefore and notAfter bounds.
// Intended use of this API is only by tests
func (ca *CA) GenerateCertificateWithDuration(cn string, notBefore, notAfter time.Time, key crypto.PublicKey) (*x509.Certificate, error) {
	return ca.generateCertificate(cn, notAfter, notAfter, key)
}

// NewCACertificate returns a self-signed certificate used as certificate authority
func NewCACertificate(key *rsa.PrivateKey) (*x509.Certificate, error) {
	max := new(big.Int).SetInt64(math.MaxInt64)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		Version:               tls.VersionTLS12,
		SerialNumber:          serial,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365).UTC(),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		DNSNames:              []string{"pmem-csi", "ca"},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	*tmpl = x509.Certificate{}
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, err
	}

	runtime.SetFinalizer(cert, func(c *x509.Certificate) {
		*c = x509.Certificate{}
	})

	return cert, nil
}

func (ca *CA) generateCertificate(cn string, notBefore, notAfter time.Time, key crypto.PublicKey) (*x509.Certificate, error) {
	max := new(big.Int).SetInt64(math.MaxInt64)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		Version:      tls.VersionTLS12,
		SerialNumber: serial,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key, ca.prKey)
	*tmpl = x509.Certificate{}
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, err
	}

	runtime.SetFinalizer(cert, func(c *x509.Certificate) {
		*c = x509.Certificate{}
	})

	return cert, nil
}
