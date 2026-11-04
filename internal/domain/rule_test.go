package domain_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

var _ruleCats = map[int64]domain.Category{
	10: {ID: 10, Kind: domain.KindExpense, Name: "Food"},
	11: {ID: 11, Kind: domain.KindExpense, Name: "Cafe"},
	12: {ID: 12, Kind: domain.KindExpense, Name: "Old", Archived: true},
	20: {ID: 20, Kind: domain.KindIncome, Name: "Salary"},
}

func cond(field domain.RuleField, op domain.CondOp, values ...string) domain.Condition {
	return domain.Condition{Field: field, Op: op, Values: values}
}

func setCategory(id int64) domain.Action {
	return domain.Action{Type: domain.ActionSetCategory, Value: strconv.FormatInt(id, 10)}
}

func TestConditionMatch(t *testing.T) {
	t.Parallel()

	in := domain.OperationInput{
		Type:        domain.OpExpense,
		AccountID:   1,
		CategoryID:  10,
		Amount:      money.MustParse("100"),
		Payee:       "Пятёрочка 123",
		SourcePayee: "PYATEROCHKA 123 MOSCOW",
		Note:        "lunch",
		MCC:         "5411",
	}
	tests := []struct {
		give domain.Condition
		want bool
	}{
		{give: cond(domain.RuleFieldPayee, domain.CondIs, "пятерочка 123"), want: true},
		{give: cond(domain.RuleFieldPayee, domain.CondIs, "Пятёрочка")},
		{give: cond(domain.RuleFieldPayee, domain.CondIsNot, "Магнит"), want: true},
		{give: cond(domain.RuleFieldPayee, domain.CondContains, "ПЯТЁР"), want: true},
		{give: cond(domain.RuleFieldPayee, domain.CondNotContains, "пятер")},
		{give: cond(domain.RuleFieldSourcePayee, domain.CondOneOf, "x", "pyaterochka 123 moscow"), want: true},
		{give: cond(domain.RuleFieldSourcePayee, domain.CondNotOneOf, "x", "y"), want: true},
		{give: cond(domain.RuleFieldSourcePayee, domain.CondMatches, `^pyaterochka \d+`), want: true},
		{
			give: domain.Condition{Field: domain.RuleFieldSourcePayee, Op: domain.CondMatches, Values: []string{`\D{3}$`}},
			want: true,
		},
		{give: cond(domain.RuleFieldNote, domain.CondContains, "LUN"), want: true},
		{give: cond(domain.RuleFieldMCC, domain.CondOneOf, "5411", "5812"), want: true},
		{give: cond(domain.RuleFieldAmount, domain.CondIs, "100,00"), want: true},
		{give: cond(domain.RuleFieldAmount, domain.CondApprox, "107"), want: true},
		{give: cond(domain.RuleFieldAmount, domain.CondApprox, "109")},
		{give: cond(domain.RuleFieldAmount, domain.CondBetween, "50", "100"), want: true},
		{give: cond(domain.RuleFieldAmount, domain.CondGt, "100")},
		{give: cond(domain.RuleFieldAmount, domain.CondGte, "100"), want: true},
		{give: cond(domain.RuleFieldAmount, domain.CondLt, "100.01"), want: true},
		{give: cond(domain.RuleFieldAmount, domain.CondLte, "99")},
		{give: cond(domain.RuleFieldAccount, domain.CondIs, "1"), want: true},
		{give: cond(domain.RuleFieldCategory, domain.CondNotOneOf, "11", "20"), want: true},
		{give: cond(domain.RuleFieldCategory, domain.CondIsNot, "10")},
		{give: cond(domain.RuleFieldType, domain.CondIs, "income")},
	}
	for _, tt := range tests {
		t.Run(string(tt.give.Field)+" "+string(tt.give.Op)+" "+tt.give.Values[0], func(t *testing.T) {
			t.Parallel()

			rule := domain.Rule{
				ID: 1, Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll,
				Conditions: []domain.Condition{tt.give}, Actions: []domain.Action{setCategory(11)},
			}
			rs, err := domain.NewRuleSet([]domain.Rule{rule}, _ruleCats)
			require.NoError(t, err)
			assert.Equal(t, tt.want, rs.Matches(1, in))
		})
	}
}

func TestNewRule(t *testing.T) {
	t.Parallel()

	valid := domain.RuleInput{
		Stage: domain.RuleStageDefault,
		Match: domain.RuleMatchAll,
		Conditions: []domain.Condition{
			cond(domain.RuleFieldPayee, domain.CondOneOf, " Magnit ", "Pyaterochka"),
		},
		Actions: []domain.Action{setCategory(10), {Type: domain.ActionAddTag, Value: " big   groceries "}},
	}
	rule, err := domain.NewRule(valid, domain.RuleOriginUser, _ruleCats)
	require.NoError(t, err)
	assert.Equal(t, []string{"Magnit", "Pyaterochka"}, rule.Conditions[0].Values)
	assert.Equal(t, "big groceries", rule.Actions[1].Value, "cleaned as operation tags are")
	assert.Equal(t, domain.RuleOriginUser, rule.Origin)
	assert.Equal(t, " Magnit ", valid.Conditions[0].Values[0], "input is not changed")

	tests := []struct {
		name string
		give func(in *domain.RuleInput)
	}{
		{name: "stage", give: func(in *domain.RuleInput) { in.Stage = "late" }},
		{name: "match", give: func(in *domain.RuleInput) { in.Match = "some" }},
		{name: "no conditions", give: func(in *domain.RuleInput) { in.Conditions = nil }},
		{name: "no actions", give: func(in *domain.RuleInput) { in.Actions = nil }},
		{name: "field", give: func(in *domain.RuleInput) { in.Conditions[0].Field = "date" }},
		{name: "op of field", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldAmount, domain.CondContains, "1")
		}},
		{name: "between takes 2", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldAmount, domain.CondBetween, "1")
		}},
		{name: "between order", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldAmount, domain.CondBetween, "5", "1")
		}},
		{name: "amount", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldAmount, domain.CondIs, "ten")
		}},
		{name: "empty value", give: func(in *domain.RuleInput) { in.Conditions[0].Values = []string{" "} }},
		{name: "regexp", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldPayee, domain.CondMatches, "(")
		}},
		{name: "condition category", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldCategory, domain.CondIs, "99")
		}},
		{name: "type", give: func(in *domain.RuleInput) {
			in.Conditions[0] = cond(domain.RuleFieldType, domain.CondIs, "gift")
		}},
		{name: "action category", give: func(in *domain.RuleInput) { in.Actions[0] = setCategory(99) }},
		{name: "action type", give: func(in *domain.RuleInput) { in.Actions[0].Type = "delete" }},
		{name: "empty payee", give: func(in *domain.RuleInput) {
			in.Actions[0] = domain.Action{Type: domain.ActionSetPayee, Value: " "}
		}},
		{name: "tag", give: func(in *domain.RuleInput) {
			in.Actions[0] = domain.Action{Type: domain.ActionAddTag, Value: "a,b"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := valid
			in.Conditions = []domain.Condition{valid.Conditions[0]}
			in.Actions = []domain.Action{valid.Actions[0]}
			tt.give(&in)
			_, err := domain.NewRule(in, domain.RuleOriginUser, _ruleCats)
			require.ErrorIs(t, err, errors.ErrInvalid)
		})
	}
}

func TestRuleSetApply(t *testing.T) {
	t.Parallel()

	payeeIs := func(v string) []domain.Condition {
		return []domain.Condition{cond(domain.RuleFieldPayee, domain.CondIs, v)}
	}
	rules := []domain.Rule{
		// most specific, runs last and wins over rule 1
		{
			ID: 1, Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll, Conditions: payeeIs("Cafe Pushkin"),
			Actions: []domain.Action{setCategory(11), {Type: domain.ActionAddTag, Value: "eating out"}},
		},
		{
			ID: 2, Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll,
			Conditions: []domain.Condition{cond(domain.RuleFieldPayee, domain.CondContains, "cafe")},
			Actions:    []domain.Action{setCategory(10), {Type: domain.ActionAppendNote, Value: "#cafe"}},
		},
		// pre rule renames the bank payee, so default rules see the clean name
		{
			ID: 3, Stage: domain.RuleStagePre, Match: domain.RuleMatchAll,
			Conditions: []domain.Condition{cond(domain.RuleFieldSourcePayee, domain.CondContains, "PUSHKIN")},
			Actions:    []domain.Action{{Type: domain.ActionSetPayee, Value: "Cafe Pushkin"}},
		},
		// post rule of any condition
		{
			ID: 4, Stage: domain.RuleStagePost, Match: domain.RuleMatchAny,
			Conditions: []domain.Condition{
				cond(domain.RuleFieldMCC, domain.CondIs, "0000"),
				cond(domain.RuleFieldAmount, domain.CondGt, "1000"),
			},
			Actions: []domain.Action{
				{Type: domain.ActionPrependNote, Value: "big:"}, {Type: domain.ActionAddTag, Value: "Eating Out"},
			},
		},
		// archived and income categories are never set on this expense
		{
			ID: 5, Stage: domain.RuleStagePost, Match: domain.RuleMatchAll, Conditions: payeeIs("cafe pushkin"),
			Actions: []domain.Action{setCategory(12), setCategory(20)},
		},
	}
	rs, err := domain.NewRuleSet(rules, _ruleCats)
	require.NoError(t, err)
	assert.Equal(t, 5, rs.Len())

	in := domain.OperationInput{
		Type:        domain.OpExpense,
		Amount:      money.MustParse("1500"),
		Payee:       "CAFE PUSHKIN MOSKVA",
		SourcePayee: "CAFE PUSHKIN MOSKVA",
		Note:        "dinner",
		Tags:        []string{"trip"},
	}
	out, changed := rs.Apply(in)
	require.True(t, changed)
	assert.Equal(t, "Cafe Pushkin", out.Payee)
	assert.Equal(t, int64(11), out.CategoryID)
	assert.Equal(t, "big: dinner #cafe", out.Note)
	assert.Equal(t, []string{"trip", "eating out"}, out.Tags)
	assert.Equal(t, []string{"trip"}, in.Tags, "input tags are not changed")

	out, changed = rs.Apply(domain.OperationInput{Type: domain.OpExpense, Payee: "Shop", Amount: money.FromInt(1)})
	assert.False(t, changed)
	assert.Equal(t, "Shop", out.Payee)

	// a transfer gets no category, but its payee is renamed
	out, changed = rs.Apply(domain.OperationInput{
		Type: domain.OpTransfer, SourcePayee: "pushkin", Amount: money.FromInt(1),
	})
	assert.True(t, changed)
	assert.Equal(t, "Cafe Pushkin", out.Payee)
	assert.Zero(t, out.CategoryID)
}

func TestRuleSetApplyAgain(t *testing.T) {
	t.Parallel()

	rule := domain.Rule{
		ID: 1, Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll,
		Conditions: []domain.Condition{cond(domain.RuleFieldCategory, domain.CondIsNot, "11")},
		Actions: []domain.Action{
			{Type: domain.ActionAppendNote, Value: "#food"},
			{Type: domain.ActionPrependNote, Value: "big:"},
			{Type: domain.ActionAddTag, Value: "food"},
		},
	}
	rs, err := domain.NewRuleSet([]domain.Rule{rule}, _ruleCats)
	require.NoError(t, err)

	in := domain.OperationInput{Type: domain.OpExpense, CategoryID: 10, Amount: money.FromInt(1), Note: "lunch"}
	out, changed := rs.Apply(in)
	require.True(t, changed)
	assert.Equal(t, "big: lunch #food", out.Note)

	again, changed := rs.Apply(out)
	assert.False(t, changed, "the note and the tag are not repeated")
	assert.Equal(t, out, again)

	// a tag over the limit is not added, as the operation would not validate
	full := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"}
	out, _ = rs.Apply(domain.OperationInput{Type: domain.OpExpense, CategoryID: 10, Amount: money.FromInt(1), Tags: full})
	assert.Equal(t, full, out.Tags)

	// a transfer has no category, so it is not "category is not 11"
	_, changed = rs.Apply(domain.OperationInput{Type: domain.OpTransfer, Amount: money.FromInt(1)})
	assert.False(t, changed)
}

func TestFillEmpty(t *testing.T) {
	t.Parallel()

	user := domain.OperationInput{Payee: "Shop", Tags: []string{"a"}}
	ruled := domain.OperationInput{Payee: "Big Shop", Note: "ruled", CategoryID: 10, Tags: []string{"a", "b"}}
	got := domain.FillEmpty(user, ruled)
	assert.Equal(t, "Shop", got.Payee)
	assert.Equal(t, "ruled", got.Note)
	assert.Equal(t, int64(10), got.CategoryID)
	assert.Equal(t, []string{"a", "b"}, got.Tags)

	user.CategoryID, user.Note = 11, "mine"
	got = domain.FillEmpty(user, ruled)
	assert.Equal(t, int64(11), got.CategoryID)
	assert.Equal(t, "mine", got.Note)
}

func TestLearnCategory(t *testing.T) {
	t.Parallel()

	date, err := timex.ParseDate("2026-10-29")
	require.NoError(t, err)
	// history newest first: ids 5..1
	history := func(cats ...int64) []domain.Operation {
		ops := make([]domain.Operation, len(cats))
		for i, c := range cats {
			ops[i] = domain.Operation{
				ID: int64(len(cats) - i), Type: domain.OpExpense, CategoryID: c, Payee: "Shop",
				Date: date.AddDate(0, 0, -i),
			}
		}
		return ops
	}
	simple := domain.Rule{
		ID: 7, Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll,
		Conditions: []domain.Condition{cond(domain.RuleFieldPayee, domain.CondIs, "SHOP")},
		Actions:    []domain.Action{setCategory(10)},
		Origin:     domain.RuleOriginLearned,
	}

	t.Run("creates", func(t *testing.T) {
		t.Parallel()

		h := history(11, 11, 10, 11, 10)
		got, ok := domain.LearnCategory(h, h[0], nil, _ruleCats)
		require.True(t, ok)
		require.Len(t, got, 1)
		assert.Zero(t, got[0].ID)
		assert.Equal(t, domain.RuleOriginLearned, got[0].Origin)
		assert.Equal(t, domain.RuleStageDefault, got[0].Stage)
		assert.Equal(t, []domain.Condition{cond(domain.RuleFieldPayee, domain.CondIs, "Shop")},
			got[0].Conditions)
		assert.Equal(t, []domain.Action{setCategory(11)}, got[0].Actions)
	})
	t.Run("too few uses", func(t *testing.T) {
		t.Parallel()

		h := history(11, 11, 10, 10, 20, 11)
		_, ok := domain.LearnCategory(h, h[0], nil, _ruleCats)
		assert.False(t, ok, "only the 5 latest count")
	})
	t.Run("changed not among latest", func(t *testing.T) {
		t.Parallel()

		h := history(11, 11, 11, 10, 10, 10)
		_, ok := domain.LearnCategory(h, h[5], nil, _ruleCats)
		assert.False(t, ok)
	})
	t.Run("splits and transfers skipped", func(t *testing.T) {
		t.Parallel()

		h := history(11, 11, 11, 10, 10)
		h[1].OriginID = 99
		h[2].Type = domain.OpTransfer
		_, ok := domain.LearnCategory(h, h[0], nil, _ruleCats)
		assert.False(t, ok)
	})
	t.Run("updates simple rule", func(t *testing.T) {
		t.Parallel()

		h := history(11, 11, 11)
		got, ok := domain.LearnCategory(h, h[0], []domain.Rule{simple}, _ruleCats)
		require.True(t, ok)
		require.Len(t, got, 1)
		assert.Equal(t, int64(7), got[0].ID)
		assert.Equal(t, []domain.Action{setCategory(11)}, got[0].Actions)
		assert.Equal(t, []domain.Action{setCategory(10)}, simple.Actions, "rules are not changed")
	})
	t.Run("simple rule already set", func(t *testing.T) {
		t.Parallel()

		h := history(10, 10, 10)
		_, ok := domain.LearnCategory(h, h[0], []domain.Rule{simple}, _ruleCats)
		assert.False(t, ok)
	})
	t.Run("simple rule of other kind kept", func(t *testing.T) {
		t.Parallel()

		income := simple
		income.Actions = []domain.Action{setCategory(20)}
		h := history(11, 11, 11)
		got, ok := domain.LearnCategory(h, h[0], []domain.Rule{income}, _ruleCats)
		require.True(t, ok)
		assert.Zero(t, got[0].ID)
	})
	t.Run("covered by one of", func(t *testing.T) {
		t.Parallel()

		oneOf := simple
		oneOf.Conditions = []domain.Condition{cond(domain.RuleFieldPayee, domain.CondOneOf, "a", "shop")}
		oneOf.Actions = []domain.Action{setCategory(11)}
		h := history(11, 11, 11)
		_, ok := domain.LearnCategory(h, h[0], []domain.Rule{oneOf}, _ruleCats)
		assert.False(t, ok)
	})
}

func TestRuleUsesCategory(t *testing.T) {
	t.Parallel()

	rule := domain.Rule{
		Conditions: []domain.Condition{cond(domain.RuleFieldCategory, domain.CondOneOf, "10", "11")},
		Actions:    []domain.Action{setCategory(20)},
	}
	assert.True(t, rule.UsesCategory(11))
	assert.True(t, rule.UsesCategory(20))
	assert.False(t, rule.UsesCategory(12))
}
