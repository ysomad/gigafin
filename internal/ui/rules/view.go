package rules

import (
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

type rulesData struct {
	// Stages are the rules of each stage that has any, oldest first, in the order
	// stages run.
	Stages []stageRules
	// Learned is the number of rules just learned from the operations, -1 when not
	// learning.
	Learned int
	Filter  domain.RuleFilter
	// Filtered reports whether the rules are filtered, FilterCount by how many fields
	// but the query.
	Filtered    bool
	FilterCount int
	// Category is the category filtered by, 0 for all.
	Category int64
	// Shown is the number of rules matching the filter, Total of all rules.
	Shown, Total int
	Categories   []domain.Category
	Accounts     []domain.Account
}

type stageRules struct {
	Stage domain.RuleStage
	Rules []ruleView
}

// ruleView is a rule with the ids in its values replaced by names.
type ruleView struct {
	ID      int64
	Origin  domain.RuleOrigin
	Conds   []cond
	Actions []actionRow
}

type cond struct {
	Field domain.RuleField
	Op    domain.CondOp
	// Values are the values joined by _ruleValueSep.
	Values string
	// Any joins the condition to the one before with "or".
	Any bool
}

// condRow is a condition row of the rule form, with the operators of its field.
type condRow struct {
	Field domain.RuleField
	Op    domain.CondOp
	// Value is the values joined by _ruleValueSep.
	Value string
	// ID is Value as a category or account id, 0 when it is not one.
	ID  int64
	Ops []domain.CondOp
}

type actionRow struct {
	Type  domain.ActionType
	Value string
	// ID is Value as a category id, 0 when it is not one.
	ID int64
}

// ruleForm is the rule form; untagged fields are view only.
type ruleForm struct {
	ID         int64
	Origin     domain.RuleOrigin
	Stage      domain.RuleStage
	Match      domain.RuleMatch
	Conditions []condRow
	Actions    []actionRow
	// Applied is the number of operations the rule just changed, -1 when not applied.
	Applied    int
	Fields     []domain.RuleFieldOps
	Categories []domain.Category
	Accounts   []domain.Account
}

type rulePreview struct {
	// Total is how many operations the rule matches; Days hold the latest of them.
	Total    int
	Error    error
	Days     []kit.DayGroup
	CatNames map[int64]string
	AccByID  map[int64]domain.Account
}
