// Package certs embeds root certificates that are missing from system trust stores.
package certs

import _ "embed"

// RussianTrustedRootCA is the Russian Trusted Root CA of the Ministry of Digital
// Development in PEM, downloaded from
// https://gu-st.ru/content/lending/russian_trusted_root_ca_pem.crt. T-Bank hosts chain
// to it.
//
//go:embed russian_trusted_root_ca.pem
var RussianTrustedRootCA []byte
