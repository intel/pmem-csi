package utils

import (
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
)

const (
	rasKeySize = 2048
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

func NewPrivateKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, rasKeySize)
}

func NewEncodedPrivateKey() ([]byte, error) {
	key, err := NewPrivateKey()
	if err != nil {
		return nil, err
	}
	return EncodeKey(key), nil
}

func EncodeKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func EncodeCert(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})
}

type CSR struct {
	rawBytes   []byte
	key        *rsa.PrivateKey
	commonName string
}

func (csr *CSR) Raw() []byte {
	return csr.rawBytes
}

func (csr *CSR) PrivateKey() *rsa.PrivateKey {
	return csr.key
}

func (csr *CSR) EncodePrivateKey() []byte {
	return EncodeKey(csr.key)
}

func (csr *CSR) Encoded() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csr.rawBytes,
	})
}

func (csr *CSR) CommonName() string {
	return csr.commonName
}

func NewCSR(commonName string, key *rsa.PrivateKey) (*CSR, error) {
	if key == nil {
		pKey, err := NewPrivateKey()
		if err != nil {
			return nil, err
		}
		key = pKey
	}
	csrTemplate := &x509.CertificateRequest{
		SignatureAlgorithm: x509.SHA256WithRSA,
		PublicKey:          key.PublicKey,
		Subject: pkix.Name{
			CommonName: commonName,
		},
	}

	csr, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
	if err != nil {
		return nil, err
	}

	return &CSR{
		rawBytes:   csr,
		key:        key,
		commonName: commonName,
	}, nil
}
