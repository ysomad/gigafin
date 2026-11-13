package rules

import (
	"context"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// ruleStageLabel returns the translated name of rule stage s.
func ruleStageLabel(ctx context.Context, s domain.RuleStage) string {
	switch s {
	case domain.RuleStagePre:
		return kit.T(ctx, "First")
	case domain.RuleStagePost:
		return kit.T(ctx, "Last")
	case domain.RuleStageDefault:
		return kit.T(ctx, "Default")
	default:
		return string(s)
	}
}

// ruleFieldLabel returns the translated name of rule field f.
func ruleFieldLabel(ctx context.Context, f domain.RuleField) string {
	switch f {
	case domain.RuleFieldSourcePayee:
		return kit.T(ctx, "Bank payee")
	case domain.RuleFieldPayee:
		return kit.T(ctx, "Payee")
	case domain.RuleFieldNote:
		return kit.T(ctx, "Note")
	case domain.RuleFieldMCC:
		return "MCC"
	case domain.RuleFieldAmount:
		return kit.T(ctx, "Amount")
	case domain.RuleFieldAccount:
		return kit.T(ctx, "Account")
	case domain.RuleFieldCategory:
		return kit.T(ctx, "Category")
	case domain.RuleFieldType:
		return kit.T(ctx, "Type")
	default:
		return string(f)
	}
}

// ruleOpLabel returns the translated name of condition operator op.
func ruleOpLabel(ctx context.Context, op domain.CondOp) string {
	switch op {
	case domain.CondIs:
		return kit.T(ctx, "is")
	case domain.CondIsNot:
		return kit.T(ctx, "is not")
	case domain.CondContains:
		return kit.T(ctx, "contains")
	case domain.CondNotContains:
		return kit.T(ctx, "does not contain")
	case domain.CondOneOf:
		return kit.T(ctx, "is one of")
	case domain.CondNotOneOf:
		return kit.T(ctx, "is not one of")
	case domain.CondMatches:
		return kit.T(ctx, "matches regexp")
	case domain.CondApprox:
		return kit.T(ctx, "is about")
	case domain.CondBetween:
		return kit.T(ctx, "is between")
	case domain.CondGt:
		return ">"
	case domain.CondGte:
		return "≥"
	case domain.CondLt:
		return "<"
	case domain.CondLte:
		return "≤"
	default:
		return string(op)
	}
}

// ruleActionLabel returns the translated name of rule action type a.
func ruleActionLabel(ctx context.Context, a domain.ActionType) string {
	switch a {
	case domain.ActionSetCategory:
		return kit.T(ctx, "Set category")
	case domain.ActionSetPayee:
		return kit.T(ctx, "Rename payee")
	case domain.ActionSetNote:
		return kit.T(ctx, "Set note")
	case domain.ActionAppendNote:
		return kit.T(ctx, "Append to note")
	case domain.ActionPrependNote:
		return kit.T(ctx, "Prepend to note")
	case domain.ActionAddTag:
		return kit.T(ctx, "Add tag")
	default:
		return string(a)
	}
}
