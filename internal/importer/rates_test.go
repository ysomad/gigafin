package importer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
)

func TestParseRates(t *testing.T) {
	t.Parallel()

	in := "\ufeffRate,To,From,Date,note\n" +
		"90.4040,RUB,usd,2024-01-10,\n" +
		"\n" +
		"0.01,EUR,RUB,2024-01-11,inverted\n"
	rates, err := importer.ParseRates(domain.RateSourceCBR, strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rates, 2)

	assert.Equal(t, domain.RateSourceCBR, rates[0].Source)
	assert.Equal(t, "USD", rates[0].Currency)
	assert.Equal(t, time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), rates[0].Date)
	assert.True(t, decimal.RequireFromString("90.404").Equal(rates[0].Rate))

	assert.Equal(t, "EUR", rates[1].Currency, "a rate from the pivot is inverted")
	assert.True(t, decimal.NewFromInt(100).Equal(rates[1].Rate))
}

func TestParseRatesErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"missing column", "date,from,to\n2024-01-10,USD,RUB\n", `missing column "rate"`},
		{"no pivot", "date,from,to,rate\n2024-01-10,USD,EUR,0.9\n", "line 2: rate: USD/EUR"},
		{"same currency", "date,from,to,rate\n2024-01-10,RUB,RUB,1\n", "line 2: rate: RUB/RUB"},
		{"bad date", "date,from,to,rate\n10.01.2024,USD,RUB,90\n", "line 2: date"},
		{"decimal comma", "date,from,to,rate\n2024-01-10,USD,RUB,\"90,4\"\n", "line 2: rate"},
		{"zero rate", "date,from,to,rate\n2024-01-10,USD,RUB,0\n", "must be positive"},
		{"unknown currency", "date,from,to,rate\n2024-01-10,XXX,RUB,1\n", "unknown currency"},
		{"empty", "date,from,to,rate\n", "no rates in file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := importer.ParseRates(domain.RateSourceCBR, strings.NewReader(tt.in))
			require.ErrorIs(t, err, errors.ErrInvalid)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
