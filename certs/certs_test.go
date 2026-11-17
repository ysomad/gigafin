package certs_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/certs"
)

func TestRussianTrustedRootCA(t *testing.T) {
	t.Parallel()

	block, _ := pem.Decode(certs.RussianTrustedRootCA)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	sum := sha256.Sum256(cert.Raw)
	assert.Equal(t, "d26d2d0231b7c39f92cc738512ba54103519e4405d68b5bd703e9788ca8ecf31", hex.EncodeToString(sum[:]),
		"the fingerprint published by the Ministry of Digital Development")
	assert.Equal(t, "Russian Trusted Root CA", cert.Subject.CommonName)
}
