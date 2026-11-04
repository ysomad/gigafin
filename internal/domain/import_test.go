package domain_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

func TestGuessAccountKind(t *testing.T) {
	t.Parallel()

	rules := []domain.AccountKindRule{
		{Kind: domain.AccountLoan, Pattern: regexp.MustCompile(`(?i)автокредит|\bloan\b`)},
		{Kind: domain.AccountCredit, Pattern: regexp.MustCompile(`(?i)кредит`)},
	}
	tests := []struct {
		give string
		want domain.AccountKind
	}{
		{give: "Автокредит", want: domain.AccountLoan}, // first rule wins
		{give: "Кредитная карта", want: domain.AccountCredit},
		{give: "Car LOAN", want: domain.AccountLoan},
		{give: "Loans4you", want: domain.AccountDebit}, // word boundary
		{give: "Black", want: domain.AccountDebit},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, domain.GuessAccountKind(tt.give, rules))
		})
	}
}

func TestStageRows(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rows := []domain.Record{
		{Account: "Black", Time: at, Amount: money.FromInt(-100), Currency: "RUB"},
		{Account: "Black", Time: at, Amount: money.FromInt(-200), Currency: "RUB"},
	}
	skipped := []domain.Record{{Account: "Black", Time: at, Currency: "RUB", SkipReason: "zero amount"}}
	hashes := domain.HashRows(domain.ImportSourceTBank, rows)

	b, staged := domain.StageImportRows(domain.Import{Source: domain.ImportSourceTBank}, rows, skipped, hashes,
		map[string]bool{hashes[1]: true})

	assert.Equal(t, int64(2), b.RowsTotal)
	assert.Equal(t, int64(1), b.RowsNew)
	assert.Equal(t, int64(1), b.RowsDup)
	assert.Equal(t, int64(1), b.RowsSkip)
	require.Len(t, staged, 3)
	assert.Equal(t, domain.ImportRowNew, staged[0].Status)
	assert.Equal(t, domain.ImportRowDuplicate, staged[1].Status)
	assert.Equal(t, domain.ImportRowSkipped, staged[2].Status)
}

func TestCommitRows(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := domain.ImportMapping{
		Accounts: map[string]domain.Account{
			domain.AccountKey("Black", "RUB"):   {ID: 1, Currency: "RUB"},
			domain.AccountKey("Savings", "RUB"): {ID: 2, Currency: "RUB"},
		},
		Categories: map[string]domain.Category{
			domain.CategoryKey(domain.KindExpense, "Food"): {ID: 10, Kind: domain.KindExpense},
		},
	}
	rows := []domain.ImportRow{
		{
			ID: 1, Hash: "out", Status: domain.ImportRowNew, PairID: 2,
			Row: domain.Record{Account: "Black", Time: at, Amount: money.FromInt(-50), Currency: "RUB"},
		},
		{
			ID: 2, Hash: "in", Status: domain.ImportRowNew, PairID: 1,
			Row: domain.Record{Account: "Savings", Time: at, Amount: money.FromInt(50), Currency: "RUB"},
		},
		{
			ID: 3, Hash: "food", Status: domain.ImportRowNew,
			Row: domain.Record{Account: "Black", Time: at, Amount: money.FromInt(-7), Category: "Food", Currency: "RUB"},
		},
		{
			ID: 4, Hash: "dup", Status: domain.ImportRowDuplicate,
			Row: domain.Record{Account: "Black", Time: at, Amount: money.FromInt(-7), Category: "Food", Currency: "RUB"},
		},
		{
			ID: 5, Hash: "done", Status: domain.ImportRowNew,
			Row: domain.Record{Account: "Black", Time: at, Amount: money.FromInt(-1), Category: "Food", Currency: "RUB"},
		},
	}

	commits, err := domain.CommitRows(rows, map[string]bool{"done": true}, m, time.UTC)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, domain.OpTransfer, commits[0].Op.Type)
	assert.Equal(t, int64(2), commits[0].Op.ToAccountID)
	require.NotNil(t, commits[0].Pair)
	assert.Equal(t, int64(2), commits[0].Pair.ID)
	assert.Equal(t, domain.OpExpense, commits[1].Op.Type)
	assert.Nil(t, commits[1].Pair)
	assert.Equal(t, int64(10), commits[1].Op.CategoryID)
	assert.True(t, money.FromInt(7).Equal(commits[1].Op.Amount))
	rows[2].Row.Category = "Unmapped"
	_, err = domain.CommitRows(rows, nil, m, time.UTC)
	require.ErrorIs(t, err, errors.ErrConflict)
}

func TestPreviewCheckReady(t *testing.T) {
	t.Parallel()

	require.NoError(t, domain.Preview{Accounts: []domain.AccountNeed{{AccountID: 1}}}.CheckReady())
	err := domain.Preview{Categories: []domain.CategoryNeed{{Name: "Food"}}}.CheckReady()
	require.ErrorIs(t, err, errors.ErrConflict)
}

func TestPreviewMapped(t *testing.T) {
	t.Parallel()

	p := domain.Preview{
		Accounts:   []domain.AccountNeed{{AccountID: 1}},
		Categories: []domain.CategoryNeed{{Name: "Food"}, {CategoryID: 2}},
	}
	assert.True(t, p.AccountsMapped())
	assert.False(t, p.CategoriesMapped())
	assert.False(t, p.Ready())

	p.Categories[0].CategoryID = 3
	assert.True(t, p.CategoriesMapped())
	assert.True(t, p.Ready())
	assert.True(t, domain.Preview{}.Ready(), "nothing to map")
}

func TestImportCheckDiscard(t *testing.T) {
	t.Parallel()

	require.NoError(t, domain.Import{Status: domain.ImportStaged}.CheckDiscard())
	require.NoError(t, domain.Import{Status: domain.ImportUndone}.CheckDiscard())
	require.ErrorIs(t, domain.Import{Status: domain.ImportCommitted}.CheckDiscard(), errors.ErrConflict)
}

func TestParseSource(t *testing.T) {
	t.Parallel()

	for _, src := range domain.ImportSources() {
		got, err := domain.ParseImportSource(string(src))
		require.NoError(t, err)
		assert.Equal(t, src, got)
	}
	_, err := domain.ParseImportSource("sber")
	require.ErrorIs(t, err, errors.ErrInvalid)
}

func TestRecordTransfer(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := domain.ImportMapping{
		Accounts: map[string]domain.Account{
			domain.AccountKey("Black", "RUB"):   {ID: 1, Currency: "RUB"},
			domain.AccountKey("Savings", "RUB"): {ID: 2, Currency: "RUB"},
		},
	}
	rows := []domain.ImportRow{
		{
			ID: 1, Hash: "tr", Status: domain.ImportRowNew,
			Row: domain.Record{
				Account: "Black", ToAccount: "Savings", Time: at, DateOnly: true,
				Amount: money.FromInt(-50), Currency: "RUB",
			},
		},
		{
			ID: 2, Hash: "in", Status: domain.ImportRowNew,
			Row: domain.Record{
				Account:  "Savings",
				Time:     at,
				Amount:   money.FromInt(50),
				Currency: "RUB",
				Category: "Gift",
			},
		},
	}

	assert.Empty(t, domain.PairTransfers(rows), "a transfer record is not paired again")
	split := []domain.ImportRow{
		{ID: 3, Status: domain.ImportRowNew, Row: domain.Record{
			Account: "Black", Time: at, Amount: money.FromInt(-50), Currency: "RUB", Group: "1",
		}},
		{ID: 4, Status: domain.ImportRowNew, Row: domain.Record{
			Account: "Savings", Time: at, Amount: money.FromInt(50), Currency: "RUB",
		}},
	}
	assert.Empty(t, domain.PairTransfers(split), "a part of a split is not a transfer")

	p := domain.NewPreview(domain.Import{}, rows, m, nil)
	require.Len(t, p.Accounts, 2)
	assert.Equal(t, "Savings", p.Accounts[1].Name)
	assert.Equal(t, 2, p.Accounts[1].Rows, "destination of the transfer and the income")
	require.Len(t, p.Categories, 1, "the transfer needs no category")
	assert.Equal(t, "Gift", p.Categories[0].Name)

	commits, err := domain.CommitRows(rows[:1], nil, m, time.UTC)
	require.NoError(t, err)
	require.Len(t, commits, 1)
	assert.Equal(t, domain.OpTransfer, commits[0].Op.Type)
	assert.Equal(t, int64(1), commits[0].Op.AccountID)
	assert.Equal(t, int64(2), commits[0].Op.ToAccountID)
	assert.Equal(t, "50", commits[0].Op.Amount.String())
	assert.Empty(t, commits[0].Op.Time, "date only")
	assert.Nil(t, commits[0].Pair)

	plain := rows[0].Row
	plain.ToAccount = ""
	assert.NotEqual(t, plain.Hash(domain.ImportSourceZenMoney, 0), rows[0].Row.Hash(domain.ImportSourceZenMoney, 0))
}

func TestPreviewSuggest(t *testing.T) {
	t.Parallel()

	p := domain.Preview{
		Accounts: []domain.AccountNeed{
			{Name: "Black", Currency: "RUB"},
			{Name: "Black", Currency: "USD"},
			{Name: "Old", Currency: "RUB"},
			{Name: "Savings", Currency: "RUB", AccountID: 9},
		},
		Categories: []domain.CategoryNeed{
			{Name: "Food / Groceries", Kind: domain.KindExpense},
			{Name: "Food", Kind: domain.KindIncome},
			{Name: "Taxi", Kind: domain.KindExpense},
		},
	}
	accounts := []domain.Account{
		{ID: 1, Name: "Black", Currency: "RUB"},
		{ID: 2, Name: "Old", Currency: "RUB", Archived: true},
		{ID: 3, Name: "Savings", Currency: "RUB"},
	}
	tree := []domain.Category{{
		ID: 10, Kind: domain.KindExpense, Name: "Food",
		Children: []domain.Category{{ID: 11, ParentID: 10, Kind: domain.KindExpense, Name: "Groceries"}},
	}}

	got := p.Suggest(accounts, tree)
	assert.Equal(t, int64(1), got.Accounts[0].AccountID)
	assert.Zero(t, got.Accounts[1].AccountID, "other currency")
	assert.Zero(t, got.Accounts[2].AccountID, "archived")
	assert.Equal(t, int64(9), got.Accounts[3].AccountID, "saved mapping wins")
	assert.Equal(t, int64(11), got.Categories[0].CategoryID, "by full name")
	assert.Zero(t, got.Categories[1].CategoryID, "other kind")
	assert.Zero(t, got.Categories[2].CategoryID)
	assert.Zero(t, p.Accounts[0].AccountID, "p is not changed")
}

func TestPairTransfersSkipsDateOnly(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rows := []domain.ImportRow{
		{ID: 1, Status: domain.ImportRowNew, Row: domain.Record{
			Account: "Cash", Time: day, DateOnly: true, Amount: money.FromInt(-500), Currency: "RUB",
		}},
		{ID: 2, Status: domain.ImportRowNew, Row: domain.Record{
			Account: "Card", Time: day, DateOnly: true, Amount: money.FromInt(500), Currency: "RUB",
		}},
	}
	assert.Empty(t, domain.PairTransfers(rows), "same-day expense and refund are not a transfer")

	rows[0].Row.DateOnly, rows[1].Row.DateOnly = false, false
	assert.Equal(t, [][2]int64{{1, 2}}, domain.PairTransfers(rows))
}

func TestRecordHashTimeZone(t *testing.T) {
	t.Parallel()

	novosibirsk := time.FixedZone("UTC+7", 7*60*60)
	rec := func(loc *time.Location) domain.Record {
		return domain.Record{
			Account: "Card", Time: time.Date(2026, 10, 1, 9, 30, 0, 0, loc),
			Amount: money.FromInt(-1), Currency: "RUB",
		}
	}
	assert.Equal(t, rec(time.UTC).Hash(domain.ImportSourceZenMoney, 0),
		rec(novosibirsk).Hash(domain.ImportSourceZenMoney, 0), "wall clock of the file")
	assert.NotEqual(t, rec(time.UTC).Hash(domain.ImportSourceTBank, 0),
		rec(novosibirsk).Hash(domain.ImportSourceTBank, 0), "instants")
}

func TestCommitRowsPairImportedSince(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := domain.ImportMapping{Accounts: map[string]domain.Account{
		domain.AccountKey("Black", "RUB"):   {ID: 1, Currency: "RUB"},
		domain.AccountKey("Savings", "RUB"): {ID: 2, Currency: "RUB"},
	}}
	rows := []domain.ImportRow{
		{
			ID: 1, Hash: "out", Status: domain.ImportRowNew, PairID: 2,
			Row: domain.Record{Account: "Black", Time: at, Amount: money.FromInt(-50), Currency: "RUB"},
		},
		{
			ID: 2, Hash: "in", Status: domain.ImportRowNew, PairID: 1,
			Row: domain.Record{Account: "Savings", Time: at, Amount: money.FromInt(50), Currency: "RUB"},
		},
	}
	_, err := domain.CommitRows(rows, map[string]bool{"in": true}, m, time.UTC)
	require.ErrorIs(t, err, errors.ErrConflict, "the outgoing side is not lost")

	_, err = domain.CommitRows(rows, nil, domain.ImportMapping{}, time.UTC)
	require.ErrorIs(t, err, errors.ErrConflict, "unmapped accounts")
}

func TestBuildPreviewAccountPerCurrency(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rows := []domain.ImportRow{
		{ID: 1, Status: domain.ImportRowNew, Row: domain.Record{
			Account: "Black", Time: at, Amount: money.FromInt(10), Currency: "RUB", ToAccount: "Savings",
		}},
		{ID: 2, Status: domain.ImportRowNew, Row: domain.Record{
			Account: "Black", Time: at, Amount: money.FromInt(10), Currency: "USD", ToAccount: "Savings",
		}},
	}
	m := domain.ImportMapping{Accounts: map[string]domain.Account{
		domain.AccountKey("Black", "RUB"):   {ID: 1, Currency: "RUB"},
		domain.AccountKey("Black", "USD"):   {ID: 2, Currency: "USD"},
		domain.AccountKey("Savings", "RUB"): {ID: 3, Currency: "RUB"},
		domain.AccountKey("Savings", "USD"): {ID: 4, Currency: "USD"},
	}}
	p := domain.NewPreview(domain.Import{}, rows, m, nil)
	require.Len(t, p.Accounts, 4)
	assert.True(t, p.Ready())

	commits, err := domain.CommitRows(rows, nil, m, time.UTC)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, int64(2), commits[1].Op.AccountID)
	assert.Equal(t, int64(4), commits[1].Op.ToAccountID)
}
