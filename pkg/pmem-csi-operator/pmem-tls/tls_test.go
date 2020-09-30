/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/
package pmemtls_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math"
	"math/big"
	"testing"
	"time"

	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	"github.com/stretchr/testify/assert"
)

func generateSelfSignedCertificate(key *rsa.PrivateKey, keyUsage x509.KeyUsage, isCA bool) (*x509.Certificate, error) {
	max := new(big.Int).SetInt64(math.MaxInt64)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		Version:               tls.VersionTLS12,
		SerialNumber:          serial,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365),
		KeyUsage:              keyUsage,
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		Subject: pkix.Name{
			CommonName: "test root certificate authority",
		},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}

	return x509.ParseCertificate(certBytes)
}

func TestPmemTLS(t *testing.T) {
	t.Run("key", func(t *testing.T) {
		// create keys
		key, err := pmemtls.NewPrivateKey()
		assert.Empty(t, err, "Key creation failed with error: %v", err)
		assert.NotEmpty(t, key, "nil key")

		// Encode-decode
		bytes := pmemtls.EncodeKey(key)
		assert.NotEqual(t, 0, len(bytes), "Zero length")
		decodedKey, err := pmemtls.DecodeKey(bytes)
		assert.Empty(t, err, "Failed to decode key: %v", err)
		assert.Equal(t, key, decodedKey, "Mismatched key after decode: %+v", decodedKey)
	})

	t.Run("ca with defaults", func(t *testing.T) {
		ca, err := pmemtls.NewCA(nil, nil)

		assert.Empty(t, err, "CA creation with defaults failed", err)
		assert.NotEmpty(t, ca, "nil CA")
		assert.NotEmpty(t, ca.PrivateKey(), "CA: empty private key")
		assert.NotEmpty(t, ca.Certificate(), "CA: empty root certificate key")
	})

	t.Run("ca with invalid arguments", func(t *testing.T) {
		cakey, err := pmemtls.NewPrivateKey()
		assert.Empty(t, err, "failed to create new key")

		keyUsage := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
		cacert, err := generateSelfSignedCertificate(cakey, keyUsage, true)

		_, err = pmemtls.NewCA(cacert, cakey)
		assert.NotEmpty(t, err, "expected an error when no private key provided")

		keyUsage = x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
		cacert, err = generateSelfSignedCertificate(cakey, keyUsage, false)

		_, err = pmemtls.NewCA(cacert, nil)
		assert.NotEmpty(t, err, "expected an error when no private key provided")

		_, err = pmemtls.NewCA(cacert, cakey)
		assert.NotEmpty(t, err, "expected an error when provided certificate is not for CA")
	})

	t.Run("ca with provided private key", func(t *testing.T) {
		cakey, err := rsa.GenerateKey(rand.Reader, 1024)
		assert.Empty(t, err, "failed to create new key")

		encKey := pmemtls.EncodeKey(cakey)
		assert.NotEmpty(t, encKey, "Encoding key failed")

		ca, err := pmemtls.NewCA(nil, cakey)
		assert.Empty(t, err, "CA creation with pre-provisioned key failed")
		assert.NotEmpty(t, ca, "nil CA")
		assert.Equal(t, cakey, ca.PrivateKey(), "CA: mismatched private key")
		assert.Equal(t, encKey, ca.EncodedKey(), "CA: mismatched encoded key")
		assert.NotEmpty(t, ca.Certificate(), "CA: empty root certificate key")

		prKey, err := pmemtls.NewPrivateKey()
		assert.Empty(t, err, "Failed to create private key")

		cert, err := ca.GenerateCertificate("Some name", prKey.Public())
		assert.Empty(t, err, "Failed to sign certificate")
		assert.NotEmpty(t, cert, "Generated certificate is empty")
	})

	t.Run("ca with provided certificate and key", func(t *testing.T) {
		cakey, err := pmemtls.NewPrivateKey()
		assert.Empty(t, err, "failed to create new key")

		keyUsage := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
		cacert, err := generateSelfSignedCertificate(cakey, keyUsage, true)

		ca, err := pmemtls.NewCA(cacert, cakey)
		assert.Empty(t, err, "CA creation with pre-provisioned key failed")
		assert.NotEmpty(t, ca, "nil CA")
		assert.Equal(t, cakey, ca.PrivateKey(), "CA: mismatched private key")
		assert.Equal(t, cacert, ca.Certificate(), "CA: empty root certificate key")

		prKey, err := pmemtls.NewPrivateKey()
		assert.Empty(t, err, "Failed to create private key")

		// CA signing truncates noano seconds
		validity := time.Now().Add(time.Hour * 24 * 365).UTC().Truncate(time.Second)

		cert, err := ca.GenerateCertificate("test-cert", prKey.Public())
		assert.Empty(t, err, "Failed to sign certificate")
		assert.NotEmpty(t, cert, "Generated certificate is empty")

		isValid := cert.NotAfter.Equal(validity) || cert.NotAfter.After(validity)
		assert.Equal(t, isValid, true, "invalid certificate validity(%v) expected least %v", cert.NotAfter, validity)

		assert.Contains(t, cert.DNSNames, "test-cert", "mismatched common name")
	})
}
