package view

import "github.com/ysomad/gigafin/internal/domain"

type Rules struct {
	// Stages are the rules of each stage that has any, oldest first, in the order
	// stages run.
	Stages []RuleStage
}

type RuleStage struct {
	Stage domain.RuleStage
	Rules []Rule
}

// Rule is a rule with the ids in its values replaced by names.
type Rule struct {
	ID      int64
	Origin  domain.RuleOrigin
	Conds   []Cond
	Actions []ActionRow
}

type Cond struct {
	Field domain.RuleField
	Op    domain.CondOp
	// Values are the values joined by _ruleValueSep.
	Values string
	// Any joins the condition to the one before with "or".
	Any bool
}

// CondRow is a condition row of the rule form, with the operators of its field.
type CondRow struct {
	Field domain.RuleField
	Op    domain.CondOp
	// Value is the values joined by _ruleValueSep.
	Value string
	// ID is Value as a category or account id, 0 when it is not one.
	ID  int64
	Ops []domain.CondOp
}

type ActionRow struct {
	Type  domain.ActionType
	Value string
	// ID is Value as a category id, 0 when it is not one.
	ID int64
}

// RuleForm is the rule form; untagged fields are view only.
type RuleForm struct {
	ID         int64
	Origin     domain.RuleOrigin
	Stage      domain.RuleStage
	Match      domain.RuleMatch
	Conditions []CondRow
	Actions    []ActionRow
	// Applied is the number of operations the rule just changed, -1 when not applied.
	Applied    int
	Fields     []domain.RuleFieldOps
	Categories []domain.Category
	Accounts   []domain.Account
}

type RulePreview struct {
	// Total is how many operations the rule matches; Days hold the latest of them.
	Total    int
	Error    error
	Days     []DayGroup
	CatNames map[int64]string
	AccByID  map[int64]domain.Account
}
