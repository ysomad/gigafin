package importer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
)

const _zenMoneyHeader = "date;categoryName;payee;comment;outcomeAccountName;outcome;outcomeCurrencyShortTitle;" +
	"incomeAccountName;income;incomeCurrencyShortTitle;createdDate;changedDate;qrCode"

func TestZenMoneyParse(t *testing.T) {
	t.Parallel()

	csv := "\ufeff" + _zenMoneyHeader + "\n" +
		`2026-10-29;"Groceries / Delivery";"SAMOKAT";;"Black";"1040.5";RUB;"Black";"0";RUB;"2026-10-29 22:48:38";;` + "\n" +
		`2026-10-28;;"Avito";"refund";"Black";"0";RUB;"Black";"356";RUB;"2026-10-28 20:59:16";"2026-10-29 04:00:39";` + "\n" +
		`2026-10-27;;;;"Black";"24000";RUB;"Savings";"24000";RUB;"2026-10-30 21:59:38";"2026-10-30 22:00:27";` + "\n" +
		"\n" +
		`2026-10-26;;;;"Black";"100";RUB;"Dollars";"1";USD;"2026-10-26 10:00:00";"2026-10-26 10:00:00";` + "\n" +
		`2026-10-26;;;;"Black";"100";RUB;"Savings";"90";RUB;"2026-10-26 10:00:00";"2026-10-26 10:00:00";` + "\n" +
		`2026-10-26;"Etc";;;"Black";"0";RUB;"Black";"0";RUB;"2026-10-26 10:00:00";"2026-10-26 10:00:00";` + "\n" +
		`2026-10-25;"Home / Rent, Home / Utility";;;"Black";"1";RUB;"Black";"0";RUB;;;` + "\n" +
		`2026-10-25;"Travel / Flights / Airfare";;;"Black";"1";RUB;"Black";"0";RUB;;;` + "\n" +
		`2026-10-25;"Clothing / Accessories ";;;"Black";"1";RUB;"Black";"0";RUB;;;` + "\n"

	res, err := importer.Parse(domain.ImportSourceZenMoney, strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.Len(t, res.Rows, 6)
	assert.Equal(t, "Home / Rent", res.Rows[3].Category, "first of several tags")
	assert.Equal(t, "Travel / Flights/Airfare", res.Rows[4].Category, `" / " in a tag name`)
	assert.Equal(t, "Clothing / Accessories", res.Rows[5].Category)

	r := res.Rows[0]
	assert.Equal(t, 2, r.Line)
	assert.Equal(t, "Black", r.Account)
	assert.Equal(t, "-1040.5", r.Amount.String(), "outcome is money out")
	assert.Equal(t, "RUB", r.Currency)
	assert.Equal(t, "Groceries / Delivery", r.Category)
	assert.Equal(t, "SAMOKAT", r.Payee)
	assert.Empty(t, r.MCC, "no MCC in the payee")
	assert.Equal(t, "Avito", res.Rows[1].Payee)
	assert.Equal(t, time.Date(2026, 10, 29, 22, 48, 38, 0, time.UTC), r.Time, "clock from createdDate")
	assert.False(t, r.DateOnly)
	assert.False(t, r.IsTransfer())

	r = res.Rows[1]
	assert.Equal(t, "356", r.Amount.String())
	assert.Equal(t, "Uncategorized", r.Category)
	assert.Equal(t, "refund", r.Note)

	r = res.Rows[2]
	assert.True(t, r.IsTransfer())
	assert.Equal(t, "Black", r.Account)
	assert.Equal(t, "Savings", r.ToAccount)
	assert.Equal(t, "-24000", r.Amount.String())
	assert.Empty(t, r.Category)
	assert.True(t, r.DateOnly, "created on another day")
	assert.Equal(t, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), r.Time)

	require.Len(t, res.Skipped, 3)
	assert.Equal(t, "transfer between currencies", res.Skipped[0].SkipReason)
	assert.Equal(t, 6, res.Skipped[0].Line)
	assert.Equal(t, "transfer amounts differ", res.Skipped[1].SkipReason)
	assert.Equal(t, "zero amount", res.Skipped[2].SkipReason)
}

func TestZenMoneyParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		give   string
		want   string
		wantIs error
	}{
		{name: "empty", give: "", want: "empty file", wantIs: errors.ErrInvalid},
		{
			name:   "missing column",
			give:   "date;categoryName\n",
			want:   "missing column",
			wantIs: errors.ErrInvalid,
		},
		{
			name:   "no operations",
			give:   _zenMoneyHeader + "\n",
			want:   "no operations",
			wantIs: errors.ErrInvalid,
		},
		{
			name:   "bad date",
			give:   _zenMoneyHeader + "\n" + `29.10.2026;;;;"Black";"1";RUB;"Black";"0";RUB;;;` + "\n",
			want:   "YYYY-MM-DD",
			wantIs: errors.ErrInvalid,
		},
		{
			name:   "bad amount",
			give:   _zenMoneyHeader + "\n" + `2026-10-29;;;;"Black";"x";RUB;"Black";"0";RUB;;;` + "\n",
			want:   "line 2",
			wantIs: errors.ErrInvalid,
		},
		{
			name:   "negative",
			give:   _zenMoneyHeader + "\n" + `2026-10-29;;;;"Black";"-1";RUB;"Black";"0";RUB;;;` + "\n",
			want:   "negative",
			wantIs: errors.ErrInvalid,
		},
		{
			name:   "currency",
			give:   _zenMoneyHeader + "\n" + `2026-10-29;;;;"Black";"1";XXX;"Black";"0";XXX;;;` + "\n",
			want:   "unsupported currency",
			wantIs: errors.ErrInvalid,
		},
		{
			name:   "no account",
			give:   _zenMoneyHeader + "\n" + `2026-10-29;;;;"";"1";RUB;"";"0";RUB;;;` + "\n",
			want:   "outcomeAccountName: empty",
			wantIs: errors.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := importer.Parse(domain.ImportSourceZenMoney, strings.NewReader(tt.give), time.UTC)
			require.ErrorContains(t, err, tt.want)
			require.ErrorIs(t, err, tt.wantIs)
		})
	}
}

func TestZenMoneyParseMCC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{give: "SBER*5411*SAMOKAT", want: "5411"},
		{give: "YANDEX*4121*FASTEN", want: "4121"},
		{give: "RK*OOO DZENMANI.RU", want: ""},
		{give: "Алексей М.", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			csv := _zenMoneyHeader + "\n" + `2026-10-29;;"` + tt.give + `";;"Black";"1";RUB;"Black";"0";RUB;;;` + "\n"
			res, err := importer.Parse(domain.ImportSourceZenMoney, strings.NewReader(csv), time.UTC)
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			assert.Equal(t, tt.want, res.Rows[0].MCC)
		})
	}
}
