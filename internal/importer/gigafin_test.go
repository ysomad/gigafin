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
	"github.com/ysomad/gigafin/internal/money"
)

const _gigaFinHeader = "date;time;account;amount;currency;category;payee;note;to_account"

func TestGigaFinParse(t *testing.T) {
	t.Parallel()

	csv := "\ufeff" + _gigaFinHeader + "\n" +
		`2026-10-29;22:48;Black;-1 040,50;rub;Food / Groceries;Samokat;weekly;` + "\n" +
		`2026-10-28;;Black;356;RUB;;Avito;;` + "\n" +
		"\n" +
		`2026-10-27;21:59:38;Black;-24000;RUB;Ignored;;;Savings` + "\n" +
		`2026-10-26;;Black;0;RUB;;;;` + "\n"

	res, err := importer.Parse(domain.ImportSourceGigaFin, strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.Len(t, res.Rows, 3)

	r := res.Rows[0]
	assert.Equal(t, 2, r.Line)
	assert.Equal(t, "Black", r.Account)
	assert.Equal(t, "-1040.5", r.Amount.String())
	assert.Equal(t, "RUB", r.Currency)
	assert.Equal(t, "Food / Groceries", r.Category)
	assert.Equal(t, "Samokat", r.Payee)
	assert.Equal(t, "weekly", r.Note)
	assert.Equal(t, time.Date(2026, 10, 29, 22, 48, 0, 0, time.UTC), r.Time)
	assert.False(t, r.DateOnly)

	r = res.Rows[1]
	assert.True(t, r.DateOnly)
	assert.Equal(t, time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), r.Time)
	assert.Equal(t, "Uncategorized", r.Category)

	r = res.Rows[2]
	assert.True(t, r.IsTransfer())
	assert.Equal(t, "Savings", r.ToAccount)
	assert.Empty(t, r.Category, "transfers have no category")
	assert.Equal(t, time.Date(2026, 10, 27, 21, 59, 38, 0, time.UTC), r.Time)

	require.Len(t, res.Skipped, 1)
	assert.Equal(t, "zero amount", res.Skipped[0].SkipReason)
	assert.Equal(t, 6, res.Skipped[0].Line)
}

func TestGigaFinParseColumnOrder(t *testing.T) {
	t.Parallel()

	csv := "Currency;Amount;Account;Date;extra;MCC\nRUB;-5;Cash;2026-10-01;x;5814\n"
	res, err := importer.Parse(domain.ImportSourceGigaFin, strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	assert.Equal(t, "Cash", res.Rows[0].Account)
	assert.Equal(t, "-5", res.Rows[0].Amount.String())
	assert.Equal(t, "5814", res.Rows[0].MCC)
}

func TestGigaFinParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		give   string
		want   string
		wantIs error
	}{
		{name: "empty", give: "", want: "empty file", wantIs: errors.ErrInvalid},
		{
			name: "missing column", give: "date;account;amount\n",
			want: `missing column "currency"`, wantIs: errors.ErrInvalid,
		},
		{name: "no operations", give: _gigaFinHeader + "\n", want: "no operations", wantIs: errors.ErrInvalid},
		{
			name: "bad date", give: _gigaFinHeader + "\n29.10.2026;;Black;-1;RUB;;;;\n",
			want: "YYYY-MM-DD", wantIs: errors.ErrInvalid,
		},
		{
			name: "bad time", give: _gigaFinHeader + "\n2026-10-29;25:00;Black;-1;RUB;;;;\n",
			want: "HH:MM", wantIs: errors.ErrInvalid,
		},
		{
			name: "bad amount", give: _gigaFinHeader + "\n2026-10-29;;Black;x;RUB;;;;\n",
			want: "line 2", wantIs: errors.ErrInvalid,
		},
		{
			name: "currency", give: _gigaFinHeader + "\n2026-10-29;;Black;-1;XXX;;;;\n",
			want: "unsupported currency", wantIs: errors.ErrInvalid,
		},
		{
			name: "no account", give: _gigaFinHeader + "\n2026-10-29;;;-1;RUB;;;;\n",
			want: "account: empty", wantIs: errors.ErrInvalid,
		},
		{
			name: "bad mcc", give: "date;account;amount;currency;mcc\n2026-10-29;Black;-1;RUB;58\n",
			want: "4 digits", wantIs: errors.ErrInvalid,
		},
		{
			name: "positive transfer", give: _gigaFinHeader + "\n2026-10-29;;Black;1;RUB;;;;Savings\n",
			want: "must be negative", wantIs: errors.ErrInvalid,
		},
		{
			name: "transfer to itself", give: _gigaFinHeader + "\n2026-10-29;;Black;-1;RUB;;;;Black\n",
			want: "must differ", wantIs: errors.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := importer.Parse(domain.ImportSourceGigaFin, strings.NewReader(tt.give), time.UTC)
			require.ErrorContains(t, err, tt.want)
			require.ErrorIs(t, err, tt.wantIs)
		})
	}
}

func TestGigaFinParseSplits(t *testing.T) {
	t.Parallel()

	csv := "date;time;account;amount;currency;category;payee;origin_id\n" +
		"2026-10-16;10:48:30;Black;-40000;RUB;Home / Rent;Николай О.;1\n" +
		"2026-10-29;12:00;Black;-5;RUB;Food;Shop;\n" +
		"2026-10-16;10:48:30;Black;-2150;RUB;Home / Utility;;1\n"
	res, err := importer.Parse(domain.ImportSourceGigaFin, strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.Len(t, res.Rows, 3, "every part is a record")

	assert.Equal(t, "1", res.Rows[0].Group)
	assert.Equal(t, "-40000", res.Rows[0].Amount.String())
	assert.Equal(t, "Home / Rent", res.Rows[0].Category)
	assert.Empty(t, res.Rows[1].Group)
	assert.Equal(t, "1", res.Rows[2].Group)
	assert.Equal(t, "Home / Utility", res.Rows[2].Category)

	_, err = importer.Parse(domain.ImportSourceGigaFin,
		strings.NewReader("date;account;amount;currency;to_account;origin_id\n2026-10-16;Black;-10;RUB;Cash;1\n"), time.UTC)
	require.ErrorContains(t, err, "transfers are not split")
	require.ErrorIs(t, err, errors.ErrInvalid)
}

func TestWriteGigaFin(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	accounts := map[int64]string{1: "Black", 2: "Savings"}
	categories := map[int64]string{10: "Food / Groceries", 11: "Home", 20: "Salary"}
	ops := []domain.Operation{
		{
			ID: 1, Type: domain.OpExpense, AccountID: 1, CategoryID: 10, Currency: "RUB", Amount: money.MustParse("700.50"),
			Date: day, Time: "18:48", Payee: `Shop; "Best"`, Note: "weekly", MCC: "5411", OriginID: 1,
		},
		{
			ID: 2, Type: domain.OpExpense, AccountID: 1, CategoryID: 11, Currency: "RUB", Amount: money.MustParse("339.50"),
			Date: day, Time: "18:48", Payee: `Shop; "Best"`, Note: "lamp", MCC: "5411", OriginID: 1,
		},
		{
			ID: 3, Type: domain.OpIncome, AccountID: 1, CategoryID: 20, Currency: "RUB", Amount: money.FromInt(5000), Date: day,
			Tags: []string{"vacation 2026", "work"},
		},
		{
			ID: 4, Type: domain.OpTransfer, AccountID: 1, ToAccountID: 2, Currency: "RUB", Amount: money.FromInt(100),
			Date: day,
		},
		{ID: 5, Type: domain.OpAdjustment, AccountID: 1, Currency: "RUB", Amount: money.FromInt(-3), Date: day},
		{
			ID: 6, Type: domain.OpExpense, AccountID: 1, CategoryID: 11, Currency: "RUB", Amount: money.FromInt(10),
			Date: day.AddDate(0, 0, 1), OriginID: 1,
		},
		// the only part of its split in ops
		{
			ID: 7, Type: domain.OpExpense, AccountID: 1, CategoryID: 11, Currency: "RUB", Amount: money.FromInt(1),
			Date: day.AddDate(0, 0, 2), OriginID: 9,
		},
	}

	var b strings.Builder
	require.NoError(t, importer.WriteGigaFin(&b, ops, accounts, categories))
	assert.Equal(t, "date;time;account;amount;currency;category;payee;note;to_account;mcc;origin_id;tags\n"+
		`2026-10-29;18:48;Black;-700.50;RUB;Food / Groceries;"Shop; ""Best""";weekly;;5411;1;`+"\n"+
		`2026-10-29;18:48;Black;-339.50;RUB;Home;"Shop; ""Best""";lamp;;5411;1;`+"\n"+
		"2026-10-29;;Black;5000.00;RUB;Salary;;;;;;vacation 2026,work\n"+
		"2026-10-29;;Black;-100.00;RUB;;;;Savings;;;\n"+
		"2026-10-29;;Black;-3.00;RUB;;;;;;;\n"+
		"2026-10-30;;Black;-10.00;RUB;Home;;;;;1;\n"+
		"2026-10-31;;Black;-1.00;RUB;Home;;;;;;\n", b.String())

	res, err := importer.Parse(domain.ImportSourceGigaFin, strings.NewReader(b.String()), time.UTC)
	require.NoError(t, err)
	require.Len(t, res.Rows, 7)
	assert.Equal(t, "-700.5", res.Rows[0].Amount.String())
	assert.Equal(t, `Shop; "Best"`, res.Rows[0].Payee)
	assert.Empty(t, res.Rows[0].Tags)
	assert.Equal(t, []string{"vacation 2026", "work"}, res.Rows[2].Tags)
	assert.Equal(t, "5411", res.Rows[0].MCC)
	assert.Equal(t, "1", res.Rows[1].Group)
	assert.Equal(t, "lamp", res.Rows[1].Note)
	assert.Equal(t, "Savings", res.Rows[3].ToAccount)
	assert.Equal(t, "Uncategorized", res.Rows[4].Category)
	assert.Equal(t, "1", res.Rows[5].Group, "a part on another day stays in its split")
}
