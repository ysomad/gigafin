package service_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository/repotest"
)

// payeeRule returns a rule setting the category with catID when the payee matches
// value by op.
func payeeRule(op domain.CondOp, value string, catID int64) domain.RuleInput {
	return domain.RuleInput{
		Stage:      domain.RuleStageDefault,
		Match:      domain.RuleMatchAll,
		Conditions: []domain.Condition{{Field: domain.RuleFieldPayee, Op: op, Values: []string{value}}},
		Actions:    []domain.Action{{Type: domain.ActionSetCategory, Value: strconv.FormatInt(catID, 10)}},
	}
}

func TestRulesCRUD(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	rules := f.svc.Rules

	rule, err := rules.Create(ctx, f.userID, payeeRule(domain.CondContains, " samokat ", f.groceries.ID))
	require.NoError(t, err)
	assert.Equal(t, domain.RuleOriginUser, rule.Origin)
	assert.Equal(t, []string{"samokat"}, rule.Conditions[0].Values)

	got, err := rules.Get(ctx, f.userID, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, rule.Conditions, got.Conditions)
	assert.Equal(t, rule.Actions, got.Actions)

	_, err = rules.Create(ctx, f.userID, payeeRule(domain.CondIs, "x", 999))
	require.ErrorIs(t, err, errors.ErrInvalid)

	updated, err := rules.Update(ctx, f.userID, rule.ID, payeeRule(domain.CondIs, "Samokat", f.cafe.ID))
	require.NoError(t, err)
	assert.Equal(t, domain.CondIs, updated.Conditions[0].Op)
	assert.False(t, updated.UpdatedAt.IsZero())

	other, err := f.svc.Auth.CreateUser(ctx, "rules-other", "password1234", "UTC", "user")
	require.NoError(t, err)
	_, err = rules.Get(ctx, other.ID, rule.ID)
	require.ErrorIs(t, err, errors.ErrNotFound)

	err = f.svc.Categories.Delete(ctx, f.userID, f.cafe.ID)
	require.ErrorIs(t, err, errors.ErrConflict, "used by the rule")

	list, err := rules.List(ctx, f.userID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	require.NoError(t, rules.Delete(ctx, f.userID, rule.ID))
	require.ErrorIs(t, rules.Delete(ctx, f.userID, rule.ID), errors.ErrNotFound)
	require.NoError(t, f.svc.Categories.Delete(ctx, f.userID, f.cafe.ID))
}

func TestRulesOnCreate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	_, err := f.svc.Rules.Create(ctx, f.userID, domain.RuleInput{
		Stage:      domain.RuleStageDefault,
		Match:      domain.RuleMatchAll,
		Conditions: []domain.Condition{{Field: domain.RuleFieldPayee, Op: domain.CondContains, Values: []string{"coffee"}}},
		Actions: []domain.Action{
			{Type: domain.ActionSetCategory, Value: strconv.FormatInt(f.cafe.ID, 10)},
			{Type: domain.ActionSetNote, Value: "ruled"},
			{Type: domain.ActionAddTag, Value: "coffee"},
		},
	})
	require.NoError(t, err)

	in := domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Amount: dec("300"), Date: day(t, "2026-10-29"),
		Payee: "Coffee Like",
	}
	filled, err := f.svc.Operations.Fill(ctx, f.userID, in)
	require.NoError(t, err)
	assert.Equal(t, f.cafe.ID, filled.CategoryID)

	op, err := f.svc.Operations.Create(ctx, f.userID, in)
	require.NoError(t, err)
	assert.Equal(t, f.cafe.ID, op.CategoryID, "rules fill an empty category")
	assert.Equal(t, "ruled", op.Note)
	assert.Equal(t, []string{"coffee"}, op.Tags)

	in.CategoryID, in.Note = f.groceries.ID, "mine"
	op, err = f.svc.Operations.Create(ctx, f.userID, in)
	require.NoError(t, err)
	assert.Equal(t, f.groceries.ID, op.CategoryID, "the user's category wins")
	assert.Equal(t, "mine", op.Note)

	in.Payee, in.CategoryID = "Shop", 0
	_, err = f.svc.Operations.Create(ctx, f.userID, in)
	require.ErrorIs(t, err, errors.ErrInvalid, "no rule, no category")
}

func TestRulesOnImport(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	cats, err := e.fin.Categories.Create(ctx, e.userID, 0, domain.KindExpense, "Food")
	require.NoError(t, err)
	cafe, err := e.fin.Categories.Create(ctx, e.userID, 0, domain.KindExpense, "Cafe")
	require.NoError(t, err)
	_, err = e.fin.Rules.Create(ctx, e.userID, domain.RuleInput{
		Stage: domain.RuleStagePre,
		Match: domain.RuleMatchAll,
		Conditions: []domain.Condition{
			{Field: domain.RuleFieldSourcePayee, Op: domain.CondMatches, Values: []string{`^SHOKOLADNICA`}},
		},
		Actions: []domain.Action{{Type: domain.ActionSetPayee, Value: "Shokoladnica"}},
	})
	require.NoError(t, err)
	_, err = e.fin.Rules.Create(ctx, e.userID, payeeRule(domain.CondIs, "shokoladnica", cafe.ID))
	require.NoError(t, err)

	csv := "date;time;account;amount;currency;category;payee;mcc\n" +
		"2026-10-29;18:48;Black;-1040;RUB;Food;SAMOKAT;5411\n" +
		"2026-10-29;19:00;Black;-300;RUB;Food;SHOKOLADNICA 12 MOSCOW;5814\n"
	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceGigaFin, "x.csv", strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.NoError(t, e.imp.SaveMapping(ctx, e.userID, im.ID,
		[]domain.AccountChoice{{Name: "Black", Currency: "RUB", NewName: "Black", NewKind: domain.AccountDebit}},
		[]domain.CategoryChoice{{Name: "Food", Kind: domain.KindExpense, CategoryID: cats.ID}}))
	n, err := e.imp.Commit(ctx, e.userID, im.ID, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	ops, _, err := e.fin.Operations.List(ctx, e.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 2)
	assert.Equal(t, "Shokoladnica", ops[0].Payee)
	assert.Equal(t, "SHOKOLADNICA 12 MOSCOW", ops[0].SourcePayee)
	assert.Equal(t, cafe.ID, ops[0].CategoryID, "the rule overrides the mapping")
	assert.Equal(t, "SAMOKAT", ops[1].SourcePayee)
	assert.Equal(t, cats.ID, ops[1].CategoryID)

	_, err = e.fin.Operations.Update(ctx, e.userID, ops[0].ID, domain.OperationInput{
		Type: ops[0].Type, AccountID: ops[0].AccountID, CategoryID: cats.ID, Amount: ops[0].Amount,
		Date: ops[0].Date, Payee: "Renamed",
	})
	require.NoError(t, err)
	op, err := e.fin.Operations.Get(ctx, e.userID, ops[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "SHOKOLADNICA 12 MOSCOW", op.SourcePayee, "kept on update")
}

func TestLearnCategory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	create := func(date string, catID int64) domain.Operation {
		t.Helper()
		op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: catID, Amount: dec("100"),
			Date: day(t, date), Payee: "Samokat",
		})
		require.NoError(t, err)
		return op
	}
	learned := func() []domain.Rule {
		t.Helper()
		rules, err := f.svc.Rules.List(ctx, f.userID)
		require.NoError(t, err)
		return rules
	}

	create("2026-10-01", f.groceries.ID)
	create("2026-10-02", f.groceries.ID)
	assert.Empty(t, learned())
	third := create("2026-10-03", f.cafe.ID)
	assert.Empty(t, learned())

	// recategorizing the third makes groceries used 3 times
	_, err := f.svc.Operations.Update(ctx, f.userID, third.ID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: f.groceries.ID, Amount: dec("100"),
		Date: third.Date, Payee: "Samokat",
	})
	require.NoError(t, err)
	rules := learned()
	require.Len(t, rules, 1)
	assert.Equal(t, domain.RuleOriginLearned, rules[0].Origin)
	assert.True(t, rules[0].UsesCategory(f.groceries.ID))

	// the learned rule now fills the category
	op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Amount: dec("50"), Date: day(t, "2026-10-04"), Payee: "samokat",
	})
	require.NoError(t, err)
	assert.Equal(t, f.groceries.ID, op.CategoryID)

	// three cafes in a row move the rule to cafe
	create("2026-10-05", f.cafe.ID)
	create("2026-10-06", f.cafe.ID)
	create("2026-10-07", f.cafe.ID)
	rules = learned()
	require.Len(t, rules, 1)
	assert.True(t, rules[0].UsesCategory(f.cafe.ID))

	require.NoError(t, f.svc.Settings.Set(ctx, f.userID, domain.Settings{AutoCategoryRules: false}))
	settings, err := f.svc.Settings.Get(ctx, f.userID)
	require.NoError(t, err)
	assert.False(t, settings.AutoCategoryRules)
	create("2026-10-08", f.groceries.ID)
	create("2026-10-09", f.groceries.ID)
	create("2026-10-10", f.groceries.ID)
	rules = learned()
	require.Len(t, rules, 1)
	assert.True(t, rules[0].UsesCategory(f.cafe.ID), "learning is off")
}

func TestRulesRun(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	payees := []string{"Samokat", "Samokat", "Shop"}
	ids := make([]int64, len(payees))
	for i, payee := range payees {
		op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: f.cafe.ID, Amount: dec("10"),
			Date: day(t, "2026-10-01"), Payee: payee,
		})
		require.NoError(t, err)
		ids[i] = op.ID
	}
	require.NoError(t, f.svc.Settings.Set(ctx, f.userID, domain.Settings{AutoCategoryRules: false}))
	rule, err := f.svc.Rules.Create(ctx, f.userID, payeeRule(domain.CondIs, "samokat", f.groceries.ID))
	require.NoError(t, err)

	samokat := payeeRule(domain.CondIs, "samokat", f.groceries.ID)
	matched, total, err := f.svc.Rules.Preview(ctx, f.userID, samokat, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, matched, 1)

	n, err := f.svc.Operations.Categorize(ctx, f.userID, domain.Filter{AccountID: f.cash.ID})
	require.NoError(t, err)
	assert.Zero(t, n, "filtered out")

	n, err = f.svc.Operations.Categorize(ctx, f.userID, domain.Filter{})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	op, err := f.svc.Operations.Get(ctx, f.userID, ids[0])
	require.NoError(t, err)
	assert.Equal(t, f.groceries.ID, op.CategoryID)
	op, err = f.svc.Operations.Get(ctx, f.userID, ids[2])
	require.NoError(t, err)
	assert.Equal(t, f.cafe.ID, op.CategoryID)

	n, err = f.svc.Operations.Categorize(ctx, f.userID, domain.Filter{})
	require.NoError(t, err)
	assert.Zero(t, n, "nothing left to change")

	_, err = f.svc.Rules.Update(ctx, f.userID, rule.ID, payeeRule(domain.CondIs, "shop", f.groceries.ID))
	require.NoError(t, err)
	n, err = f.svc.Rules.Apply(ctx, f.userID, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	op, err = f.svc.Operations.Get(ctx, f.userID, ids[2])
	require.NoError(t, err)
	assert.Equal(t, f.groceries.ID, op.CategoryID)
}

func TestRulesInvalidStoredRuleSkipped(t *testing.T) {
	t.Parallel()
	d := repotest.New(t)
	svc := newServices(t, d)
	uid := newUser(t, d)
	ctx := t.Context()

	acc, err := svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)
	food, err := svc.Categories.Create(ctx, uid, 0, domain.KindExpense, "Food")
	require.NoError(t, err)
	require.NoError(t, svc.Settings.Set(ctx, uid, domain.Settings{AutoCategoryRules: false}))
	_, err = svc.Rules.Create(ctx, uid, payeeRule(domain.CondIs, "samokat", food.ID))
	require.NoError(t, err)
	// stored as is, as a rule that a later version stopped accepting would be
	_, err = d.Write().Rules.Create(ctx, uid, domain.Rule{
		Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll,
		Conditions: []domain.Condition{{Field: domain.RuleFieldPayee, Op: domain.CondMatches, Values: []string{"("}}},
		Actions:    []domain.Action{{Type: domain.ActionSetNote, Value: "x"}},
		Origin:     domain.RuleOriginUser,
	}, time.Now())
	require.NoError(t, err)

	op, err := svc.Operations.Create(ctx, uid, domain.OperationInput{
		Type: domain.OpExpense, AccountID: acc.ID, Amount: dec("10"), Date: day(t, "2026-10-01"), Payee: "Samokat",
	})
	require.NoError(t, err)
	assert.Equal(t, food.ID, op.CategoryID, "valid rules still run")
}

func TestRulesPreviewSkipsSplits(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	require.NoError(t, f.svc.Settings.Set(ctx, f.userID, domain.Settings{AutoCategoryRules: false}))
	op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: f.cafe.ID, Amount: dec("10"),
		Date: day(t, "2026-10-01"), Payee: "Market",
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Split(ctx, f.userID, op.ID, []domain.SplitPart{
		{CategoryID: f.cafe.ID, Amount: dec("6")},
		{CategoryID: f.cafe.ID, Amount: dec("4")},
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: f.cafe.ID, Amount: dec("5"),
		Date: day(t, "2026-10-02"), Payee: "Market",
	})
	require.NoError(t, err)

	in := payeeRule(domain.CondIs, "market", f.groceries.ID)
	_, total, err := f.svc.Rules.Preview(ctx, f.userID, in, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, total)

	rule, err := f.svc.Rules.Create(ctx, f.userID, in)
	require.NoError(t, err)
	n, err := f.svc.Rules.Apply(ctx, f.userID, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, total, n, "preview counts what apply changes")
}

func TestLearnCategoryOldOperation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	create := func(date string, catID int64) domain.Operation {
		t.Helper()
		op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: catID, Amount: dec("100"),
			Date: day(t, date), Payee: "Samokat",
		})
		require.NoError(t, err)
		return op
	}

	require.NoError(t, f.svc.Settings.Set(ctx, f.userID, domain.Settings{AutoCategoryRules: false}))
	create("2026-01-01", f.groceries.ID)
	create("2026-01-02", f.groceries.ID)
	old := create("2026-01-03", f.cafe.ID)
	// newer operations of the payee, more than 180 days after the old ones
	for _, date := range []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05"} {
		create(date, f.cafe.ID)
	}
	require.NoError(t, f.svc.Settings.Set(ctx, f.userID, domain.Settings{AutoCategoryRules: true}))

	_, err := f.svc.Operations.Update(ctx, f.userID, old.ID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, CategoryID: f.groceries.ID, Amount: dec("100"),
		Date: old.Date, Payee: "Samokat",
	})
	require.NoError(t, err)
	rules, err := f.svc.Rules.List(ctx, f.userID)
	require.NoError(t, err)
	require.Len(t, rules, 1, "learned from the operations around the old one")
	assert.True(t, rules[0].UsesCategory(f.groceries.ID))
}
