package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "+79991234567", want: "+79991234567"},
		{in: "+7 (999) 123-45-67", want: "+79991234567"},
		{in: "89991234567", want: "+79991234567"},
		{in: "79991234567", want: "+79991234567"},
		{in: "+375291234567", want: "+375291234567"},
		{in: "9991234567"},
		{in: ""},
		{in: "+7999123456789012"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NormalizePhone(tt.in)
			if tt.want == "" {
				require.ErrorIs(t, err, errors.ErrInvalid)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBankAccountMismatch(t *testing.T) {
	t.Parallel()

	a := domain.Account{ID: 7, Balance: money.MustParse("25596")}
	tb := domain.BankAccount{AccountID: 7, Balance: money.MustParse("25596.00")}
	assert.False(t, tb.IsMismatched(a))
	tb.Balance = money.MustParse("17255")
	assert.True(t, tb.IsMismatched(a))
	assert.False(t, domain.BankAccount{Balance: tb.Balance}.IsMismatched(a), "not linked")
	assert.False(t, domain.BankAccount{AccountID: 8, Balance: tb.Balance}.IsMismatched(a), "linked to another")
}

func TestLinkedBankAccount(t *testing.T) {
	t.Parallel()

	ts := []domain.BankAccount{{ID: 1}, {ID: 2, AccountID: 7}}
	got, ok := domain.LinkedBankAccount(ts, 7)
	assert.True(t, ok)
	assert.Equal(t, int64(2), got.ID)
	_, ok = domain.LinkedBankAccount(ts, 8)
	assert.False(t, ok)
	_, ok = domain.LinkedBankAccount(ts, 0)
	assert.False(t, ok, "an unlinked bank account syncs with no account")
}
