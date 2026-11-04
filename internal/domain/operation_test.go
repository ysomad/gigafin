package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

func TestValidMCC(t *testing.T) {
	t.Parallel()

	assert.True(t, domain.ValidMCC("5411"))
	assert.True(t, domain.ValidMCC("0742"))
	assert.False(t, domain.ValidMCC(""))
	assert.False(t, domain.ValidMCC("541"))
	assert.False(t, domain.ValidMCC("54a1"))
	assert.False(t, domain.ValidMCC("٥٤١١"), "ASCII digits only")
}

func TestBuildOperationMCC(t *testing.T) {
	t.Parallel()

	date, err := timex.ParseDate("2026-10-29")
	require.NoError(t, err)
	acc := domain.Account{ID: 1, Currency: "RUB"}
	in := domain.OperationInput{
		Type: domain.OpAdjustment, AccountID: 1, Date: date, Amount: money.FromInt(5), MCC: " 5411 ",
	}

	op, err := domain.NewOperation(in, acc, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "5411", op.MCC)

	in.MCC = "54"
	_, err = domain.NewOperation(in, acc, nil, nil, nil)
	require.ErrorIs(t, err, errors.ErrInvalid)
}

func TestBuildOperationCategory(t *testing.T) {
	t.Parallel()

	date, err := timex.ParseDate("2026-10-29")
	require.NoError(t, err)
	acc := domain.Account{ID: 1, Currency: "RUB"}
	food := domain.Category{ID: 10, Kind: domain.KindExpense, Name: "Food"}
	salary := domain.Category{ID: 20, Kind: domain.KindIncome, Name: "Salary"}
	in := domain.OperationInput{
		Type: domain.OpExpense, AccountID: 1, CategoryID: 10, Date: date, Amount: money.FromInt(5),
	}

	op, err := domain.NewOperation(in, acc, nil, &food, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(10), op.CategoryID)

	tests := map[string]struct {
		in  func(domain.OperationInput) domain.OperationInput
		cat *domain.Category
	}{
		"no category": {in: func(in domain.OperationInput) domain.OperationInput {
			in.CategoryID = 0
			return in
		}},
		"category not found": {in: func(in domain.OperationInput) domain.OperationInput { return in }},
		"wrong kind": {
			in: func(in domain.OperationInput) domain.OperationInput {
				in.CategoryID = 20
				return in
			},
			cat: &salary,
		},
		"zero amount": {
			in: func(in domain.OperationInput) domain.OperationInput {
				in.Amount = money.Amount{}
				return in
			},
			cat: &food,
		},
		"adjustment with category": {
			in: func(in domain.OperationInput) domain.OperationInput {
				in.Type = domain.OpAdjustment
				return in
			},
			cat: &food,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewOperation(tt.in(in), acc, nil, tt.cat, nil)
			require.ErrorIs(t, err, errors.ErrInvalid)
		})
	}
}

func TestSplitOperation(t *testing.T) {
	t.Parallel()

	cats := map[int64]domain.Category{
		10: {ID: 10, Kind: domain.KindExpense, Name: "Food"},
		11: {ID: 11, Kind: domain.KindExpense, Name: "Home"},
		20: {ID: 20, Kind: domain.KindIncome, Name: "Salary"},
	}
	op := domain.Operation{
		ID: 7, Type: domain.OpExpense, AccountID: 1, CategoryID: 10, Currency: "RUB",
		Amount: money.MustParse("100.50"), Payee: "Shop", Note: "weekly", MCC: "5411", ImportID: 3,
	}

	parts, err := domain.SplitOperation(op, []domain.SplitPart{
		{CategoryID: 11, Amount: money.MustParse("60.50"), Note: " lamp "},
		{CategoryID: 10, Amount: money.FromInt(40)},
	}, cats)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, int64(7), parts[0].ID)
	assert.Equal(t, int64(11), parts[0].CategoryID)
	assert.Equal(t, "lamp", parts[0].Note)
	assert.Equal(t, int64(0), parts[1].ID)
	assert.Equal(t, int64(10), parts[1].CategoryID)
	assert.Equal(t, "weekly", parts[1].Note, "empty note keeps the operation note")
	for _, p := range parts {
		assert.Equal(t, int64(7), p.OriginID)
		assert.Equal(t, "Shop", p.Payee)
		assert.Equal(t, "5411", p.MCC)
		assert.Equal(t, int64(3), p.ImportID)
	}

	// splitting a part keeps its split
	parts, err = domain.SplitOperation(parts[1], []domain.SplitPart{
		{CategoryID: 10, Amount: money.FromInt(30)}, {CategoryID: 11, Amount: money.FromInt(10)},
	}, cats)
	require.NoError(t, err)
	assert.Equal(t, int64(7), parts[1].OriginID)

	half := money.MustParse("50.25")
	tests := map[string]struct {
		op    func(domain.Operation) domain.Operation
		parts []domain.SplitPart
	}{
		"one part": {parts: []domain.SplitPart{{CategoryID: 10, Amount: op.Amount}}},
		"sum differs": {parts: []domain.SplitPart{
			{CategoryID: 10, Amount: half}, {CategoryID: 11, Amount: money.FromInt(1)},
		}},
		"unknown category": {parts: []domain.SplitPart{{CategoryID: 10, Amount: half}, {CategoryID: 99, Amount: half}}},
		"wrong kind":       {parts: []domain.SplitPart{{CategoryID: 10, Amount: half}, {CategoryID: 20, Amount: half}}},
		"too precise": {parts: []domain.SplitPart{
			{CategoryID: 10, Amount: money.MustParse("50.245")},
			{CategoryID: 11, Amount: money.MustParse("50.255")},
		}},
		"negative part": {parts: []domain.SplitPart{
			{
				CategoryID: 10,
				Amount:     money.MustParse("101.50"),
			}, {CategoryID: 11, Amount: money.FromInt(-1)},
		}},
		"transfer": {
			op: func(op domain.Operation) domain.Operation {
				op.Type, op.CategoryID = domain.OpTransfer, 0
				return op
			},
			parts: []domain.SplitPart{{CategoryID: 10, Amount: half}, {CategoryID: 11, Amount: half}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := op
			if tt.op != nil {
				o = tt.op(o)
			}
			_, err := domain.SplitOperation(o, tt.parts, cats)
			require.ErrorIs(t, err, errors.ErrInvalid)
		})
	}
}

func TestBuildOperationTags(t *testing.T) {
	t.Parallel()

	date, err := timex.ParseDate("2026-10-29")
	require.NoError(t, err)
	acc := domain.Account{ID: 1, Currency: "RUB"}
	in := domain.OperationInput{
		Type: domain.OpAdjustment, AccountID: 1, Date: date, Amount: money.FromInt(5),
		Tags: []string{" work ", "", "Vacation  2026", "WORK", "vacation 2026"},
	}

	op, err := domain.NewOperation(in, acc, nil, nil, []string{"Work"})
	require.NoError(t, err)
	assert.Equal(t, []string{"Vacation 2026", "Work"}, op.Tags, "known spelling wins, duplicates ignoring case dropped")

	for name, tags := range map[string][]string{
		"comma":    {"a,b"},
		"too long": {strings.Repeat("x", 51)},
		"too many": {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
	} {
		in.Tags = tags
		_, err = domain.NewOperation(in, acc, nil, nil, nil)
		require.ErrorIs(t, err, errors.ErrInvalid, name)
	}
}

func TestCashFlows(t *testing.T) {
	t.Parallel()

	flows := domain.CashFlows([]domain.TypeSum{
		{Type: domain.OpExpense, Currency: "USD", Total: money.FromInt(3)},
		{Type: domain.OpIncome, Currency: "RUB", Total: money.FromInt(100)},
		{Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(40)},
		{Type: domain.OpTransfer, Currency: "RUB", Total: money.FromInt(999)},
	})
	require.Len(t, flows, 2)
	assert.Equal(t, "RUB", flows[0].Currency)
	assert.Equal(t, "60", flows[0].Net().String())
	assert.Equal(t, "-3", flows[1].Net().String())
}
