package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

func TestNewAccountCreditLimit(t *testing.T) {
	t.Parallel()

	a, err := domain.NewAccount(domain.AccountInput{
		Name: "Card", Currency: "RUB", Kind: domain.AccountDebit, CreditLimit: money.FromInt(1000),
	})
	require.NoError(t, err)
	assert.True(t, a.CreditLimit.IsZero(), "dropped for other kinds")

	a, err = domain.NewAccount(domain.AccountInput{
		Name: "Card", Currency: "RUB", Kind: domain.AccountCredit, CreditLimit: money.FromInt(1000),
	})
	require.NoError(t, err)
	assert.True(t, a.CreditLimit.Equal(money.FromInt(1000)))
}

func TestAccountCheckDelete(t *testing.T) {
	t.Parallel()

	require.NoError(t, domain.Account{}.CheckDelete(0))
	require.ErrorIs(t, domain.Account{}.CheckDelete(3), errors.ErrConflict)
}

func TestTotals(t *testing.T) {
	t.Parallel()

	accounts := []domain.Account{
		{Currency: "USD", Kind: domain.AccountDebit, Balance: money.FromInt(10)},
		{Currency: "RUB", Kind: domain.AccountCash, Balance: money.FromInt(100)},
		{Currency: "RUB", Kind: domain.AccountCredit, Balance: money.FromInt(-30)},
		{Currency: "RUB", Kind: domain.AccountDebit, Balance: money.FromInt(999), Archived: true},
	}
	got := domain.Totals(accounts)
	require.Len(t, got, 2)
	assert.Equal(t, "RUB", got[0].Currency)
	assert.True(t, got[0].Assets.Equal(money.FromInt(100)))
	assert.True(t, got[0].Liabilities.Equal(money.FromInt(30)))
	assert.True(t, got[0].Net().Equal(money.FromInt(70)))
	assert.Equal(t, "USD", got[1].Currency)
}

func TestAccountCurrencies(t *testing.T) {
	t.Parallel()

	accounts := []domain.Account{
		{Currency: "USD"}, {Currency: "RUB"}, {Currency: "USD"}, {Currency: "EUR", Archived: true},
	}
	assert.Equal(t, []string{"RUB", "USD"}, domain.AccountCurrencies(accounts))
	assert.Equal(t, money.Currencies(), domain.AccountCurrencies(nil))
}
