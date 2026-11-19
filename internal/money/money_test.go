package money_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give    string
		want    string
		wantErr bool
	}{
		{give: "1040", want: "1040"},
		{give: "-1 040,50", want: "-1040.5"},
		{give: "1\u00a0234.05", want: "1234.05"},
		{give: " 0,1 ", want: "0.1"},
		{give: "", wantErr: true},
		{give: "abc", wantErr: true},
		{give: "1e2000000000", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			got, err := money.Parse(tt.give)
			if tt.wantErr {
				require.ErrorIs(t, err, errors.ErrInvalid)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestToMinor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		give     string
		currency string
		want     int64
		wantErr  error
	}{
		{name: "rub", give: "1040.5", currency: "RUB", want: 104050},
		{name: "negative", give: "-0.01", currency: "USD", want: -1},
		{name: "jpy", give: "1500", currency: "JPY", want: 1500},
		{name: "kwd", give: "1.234", currency: "KWD", want: 1234},
		{name: "too precise", give: "1.001", currency: "RUB", wantErr: errors.ErrInvalid},
		{name: "jpy fraction", give: "1.5", currency: "JPY", wantErr: errors.ErrInvalid},
		{name: "unknown", give: "1", currency: "XXX", wantErr: errors.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := money.MustParse(tt.give).ToMinor(tt.currency)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFromMinorFixed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give     int64
		currency string
		want     string
	}{
		{give: 104050, currency: "RUB", want: "1040.50"},
		{give: 1500, currency: "JPY", want: "1500"},
		{give: 1234, currency: "KWD", want: "1.234"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			a, err := money.FromMinor(tt.give, tt.currency)
			require.NoError(t, err)
			assert.Equal(t, tt.want, a.Fixed(tt.currency))
		})
	}

	_, err := money.FromMinor(1234, "XXX")
	require.ErrorIs(t, err, errors.ErrInvalid, "unknown currency")
	assert.Equal(t, "12.34", money.MustParse("12.34").Fixed("XXX"), "display falls back to two places")
}

func TestFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give     string
		currency string
		want     string
	}{
		{give: "-1234567", currency: "RUB", want: "-1\u202f234\u202f567.00"},
		{give: "999.99", currency: "USD", want: "999.99"},
		{give: "100", currency: "JPY", want: "100"},
		{give: "-0.001", currency: "RUB", want: "0.00"},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, money.MustParse(tt.give).Format(tt.currency))
		})
	}
}

func TestFormatWhole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{give: "1040.50", want: "1\u202f041"},
		{give: "1040.49", want: "1\u202f040"},
		{give: "-1040.50", want: "-1\u202f041"},
		{give: "-0.4", want: "0"},
		{give: "0", want: "0"},
		{give: "1234567.123", want: "1\u202f234\u202f567"},
		{give: "999", want: "999"},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, money.MustParse(tt.give).FormatWhole())
		})
	}
}

func TestMinorBounds(t *testing.T) {
	t.Parallel()

	a := money.MustParse("2.005")
	for _, tt := range []struct {
		got  func(string) (int64, error)
		cur  string
		want int64
	}{
		{a.CeilMinor, "RUB", 201},
		{a.FloorMinor, "RUB", 200},
		{a.CeilMinor, "JPY", 3},
		{a.FloorMinor, "JPY", 2},
		{a.Neg().CeilMinor, "RUB", -200},
	} {
		got, err := tt.got(tt.cur)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
	_, err := a.CeilMinor("XXX")
	require.ErrorIs(t, err, errors.ErrInvalid, "unknown currency")
}

func TestDiv(t *testing.T) {
	t.Parallel()

	assert.True(t, money.FromInt(5).Equal(money.FromInt(10).Div(2)))
}

func TestPercentOf(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(33), money.FromInt(1).PercentOf(money.FromInt(3)))
	assert.Equal(t, int64(150), money.FromInt(15).PercentOf(money.FromInt(10)))
	assert.Equal(t, int64(-50), money.FromInt(-5).PercentOf(money.FromInt(10)))
	assert.Zero(t, money.FromInt(5).PercentOf(money.Amount{}), "zero total")
}

func TestIsWhole(t *testing.T) {
	t.Parallel()

	assert.True(t, money.MustParse("1500.00").IsWhole())
	assert.True(t, money.MustParse("-3").IsWhole())
	assert.True(t, money.Amount{}.IsWhole())
	assert.False(t, money.MustParse("1500.50").IsWhole())
	assert.False(t, money.MustParse("-0.01").IsWhole())
}

func TestJSON(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(money.MustParse("-1040.50"))
	require.NoError(t, err)
	assert.JSONEq(t, `"-1040.5"`, string(b))

	var got money.Amount
	require.NoError(t, json.Unmarshal(b, &got))
	assert.True(t, money.MustParse("-1040.5").Equal(got))

	require.NoError(t, json.Unmarshal([]byte(`12.5`), &got))
	assert.True(t, money.MustParse("12.5").Equal(got))
}

func TestCurrencySymbol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{give: "RUB", want: "₽"},
		{give: "USD", want: "$"},
		{give: "EUR", want: "€"},
		{give: "AED", want: "AED"},
		{give: "XYZ", want: "XYZ"},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, money.CurrencySymbol(tt.give))
		})
	}
}
