/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"io/ioutil"
	"math"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
)

const (
	rasKeySize               = 2048
	namespaceEnvVar          = "WATCH_NAMESPACE"
	namespaceFile            = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	defaultOperatorNamespace = metav1.NamespaceSystem
)

func GetKubernetesVersion() (*version.Info, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return cs.Discovery().ServerVersion()
}

func GetKubeClient() (kubernetes.Interface, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(cfg)
}

// GetNamespace returns the namespace of the operator pod
// defaults to "kube-system"
func GetNamespace() string {
	ns := os.Getenv(namespaceEnvVar)
	if ns == "" {
		// If environment variable not set, give it a try to fetch it from
		// mounted filesystem by Kubernetes
		data, err := ioutil.ReadFile(namespaceFile)
		if err != nil {
			klog.Infof("Could not read namespace from %q: %v", namespaceFile, err)
		} else {
			ns = string(data)
			klog.Infof("Operator Namespace: %q", ns)
		}
	}

	if ns == "" {
		ns = defaultOperatorNamespace
	}

	return ns
}

// NewPrivateKey generate an rsa private key
func NewPrivateKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, rasKeySize)
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

	return x509.ParsePKCS1PrivateKey(block.Bytes)
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

	return x509.ParseCertificate(block.Bytes)
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

		cert, err = newCACertificate(prKey)
		if err != nil {
			return nil, err
		}
	}

	ca := &CA{
		prKey: prKey,
		cert:  cert,
	}
	return ca, nil
}

// PrivateKey returns private key used
func (ca *CA) PrivateKey() []byte {
	return EncodeKey(ca.prKey)
}

// Certificate returns root ca certificate used
func (ca *CA) Certificate() []byte {
	return EncodeCert(ca.cert)
}

// GenerateCertificate returns a new certificate signed for public key of given private key.
func (ca *CA) GenerateCertificate(cn string, key *rsa.PrivateKey) (*x509.Certificate, error) {
	max := new(big.Int).SetInt64(math.MaxInt64)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		Version:      tls.VersionTLS12,
		SerialNumber: serial,
		NotBefore:    ca.cert.NotBefore,
		NotAfter:     time.Now().Add(time.Hour * 24 * 365),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		Subject: pkix.Name{
			CommonName: cn,
		},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.prKey)
	if err != nil {
		return nil, err
	}

	return x509.ParseCertificate(certBytes)
}

func newCACertificate(key *rsa.PrivateKey) (*x509.Certificate, error) {
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
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		Subject: pkix.Name{
			CommonName: "pmem-csi operator root certificate authority",
		},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}

	return x509.ParseCertificate(certBytes)
}
