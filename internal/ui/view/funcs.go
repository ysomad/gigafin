package view

import (
	"context"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/money"
)

// The funcs below are called by the templ components with the request context, which
// holds the Prefs of the user, set for signed-out requests too.

// T translates key to the user's language, then formats it with args, if any.
func T(ctx context.Context, key string, args ...any) string {
	p := PrefsFrom(ctx)
	return p.Dicts.T(p.Lang, key, args...)
}

// Lang returns the user's language.
func Lang(ctx context.Context) string {
	p := PrefsFrom(ctx)
	return p.Lang
}

// ShowMinorUnits reports whether amounts are shown with minor units by the user's settings.
func ShowMinorUnits(ctx context.Context) bool {
	p := PrefsFrom(ctx)
	return p.Settings.ShowMinorUnits
}

// Money formats d with or without minor units by the user's settings.
func Money(ctx context.Context, d money.Amount, currency string) string {
	if ShowMinorUnits(ctx) {
		return d.Format(currency)
	}
	return d.FormatWhole()
}

// AmountInput drops zero minor units from an amount input value, unless the user's
// settings show them; it keeps non-zero ones, as saving a rounded value would change
// the amount.
func AmountInput(ctx context.Context, v string) string {
	if ShowMinorUnits(ctx) {
		return v
	}
	if d, err := money.Parse(v); err == nil && d.IsWhole() {
		return d.String()
	}
	return v
}

// ErrMsg returns the message of err translated to the user's language.
func ErrMsg(ctx context.Context, err error) string {
	p := PrefsFrom(ctx)
	return ErrText(p.Dicts, p.Lang, err)
}

// DayTitle formats tm as a day title, such as "Mon, 2 Jan 2006".
func DayTitle(ctx context.Context, tm time.Time) string {
	return T(ctx, tm.Format("Mon")) + tm.Format(", 2 ") + T(ctx, tm.Format("Jan")+i18n.ContextSep+"date") +
		tm.Format(" 2006")
}

// Datetime formats tm in the time zone of user, if any.
func Datetime(ctx context.Context, tm time.Time, user *domain.User) string {
	if user != nil {
		tm = tm.In(user.Location())
	}
	return tm.Format("2 ") + T(ctx, tm.Format("Jan")+i18n.ContextSep+"date") + tm.Format(" 2006 15:04")
}

// MonthName returns the translated full name of the month of tm and its year.
func MonthName(ctx context.Context, tm time.Time) string {
	return T(ctx, tm.Format("January")) + " " + tm.Format("2006")
}

// KindLabel returns the translated plural name of the categories of kind.
func KindLabel(ctx context.Context, kind domain.Kind) string {
	if kind == domain.KindExpense {
		return T(ctx, "Expenses")
	}
	return T(ctx, "Income")
}

// Invalid returns the attributes of an input with the message err: aria-invalid, and
// aria-describedby when errID, the id of the message, is set.
func Invalid(err, errID string) templ.Attributes {
	switch {
	case err == "":
		return nil
	case errID == "":
		return templ.Attributes{"aria-invalid": "true"}
	default:
		return templ.Attributes{"aria-invalid": "true", "aria-describedby": errID}
	}
}

// ErrText returns the message of err translated to lang by d: the format and
// domain.Msg args of domain.Error translated, the error kinds ending them dropped,
// other errors as is.
func ErrText(d i18n.Dicts, lang string, err error) string {
	if err == nil {
		return ""
	}

	e, ok := errors.AsType[domain.Error](err)
	if !ok {
		return err.Error()
	}

	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = a
		if m, ok := a.(domain.Msg); ok {
			args[i] = d.T(lang, string(m))
		}
	}

	msg := d.T(lang, e.Format, args...)

	if _, ok := e.Cause.(errors.Error); ok {
		return msg
	}

	return msg + ": " + ErrText(d, lang, e.Cause)
}

// OpTitle is the title of op in a list: its payee, else its kind or category.
func OpTitle(ctx context.Context, op domain.Operation, names map[int64]string) string {
	switch {
	case op.Payee != "":
		return op.Payee
	case op.Type == domain.OpTransfer:
		return T(ctx, "Transfer")
	case op.Type == domain.OpAdjustment:
		return T(ctx, "Balance adjustment")
	default:
		return names[op.CategoryID]
	}
}

// OpSubtitle is the line under the title of op in a list: its category, accounts and note.
func OpSubtitle(
	ctx context.Context, op domain.Operation, names map[int64]string, accounts map[int64]domain.Account,
) string {
	acc := accounts[op.AccountID].Name
	var parts []string
	switch op.Type {
	case domain.OpTransfer:
		parts = append(parts, acc+" → "+accounts[op.ToAccountID].Name)
	case domain.OpAdjustment:
		parts = append(parts, acc)
	case domain.OpExpense, domain.OpIncome:
		if op.Payee != "" {
			parts = append(parts, names[op.CategoryID])
		}
		if op.IsSplit() {
			parts = append(parts, T(ctx, "split"))
		}
		parts = append(parts, acc)
	}
	if op.Note != "" {
		parts = append(parts, op.Note)
	}
	return strings.Join(parts, " · ")
}

// OpAmount formats the amount of op signed: an expense negative, a positive amount
// of other than a transfer with a plus.
func OpAmount(ctx context.Context, op domain.Operation) string {
	d := op.Amount
	switch op.Type {
	case domain.OpExpense:
		d = d.Neg()
	case domain.OpIncome, domain.OpAdjustment, domain.OpTransfer:
	}
	s := Money(ctx, d, op.Currency)
	if d.IsPositive() && op.Type != domain.OpTransfer {
		s = "+" + s
	}
	return s
}

// RuleStageLabel returns the translated name of rule stage s.
func RuleStageLabel(ctx context.Context, s domain.RuleStage) string {
	switch s {
	case "pre":
		return T(ctx, "First")
	case "post":
		return T(ctx, "Last")
	default:
		return T(ctx, "Default")
	}
}

// RuleFieldLabel returns the translated name of rule field f.
func RuleFieldLabel(ctx context.Context, f domain.RuleField) string {
	switch f {
	case "source_payee":
		return T(ctx, "Bank payee")
	case "payee":
		return T(ctx, "Payee")
	case "note":
		return T(ctx, "Note")
	case "mcc":
		return "MCC"
	case "amount":
		return T(ctx, "Amount")
	case "account":
		return T(ctx, "Account")
	case "category":
		return T(ctx, "Category")
	case "type":
		return T(ctx, "Type")
	default:
		return string(f)
	}
}

// RuleOpLabel returns the translated name of condition operator op.
func RuleOpLabel(ctx context.Context, op domain.CondOp) string {
	switch op {
	case "is":
		return T(ctx, "is")
	case "is_not":
		return T(ctx, "is not")
	case "contains":
		return T(ctx, "contains")
	case "not_contains":
		return T(ctx, "does not contain")
	case "one_of":
		return T(ctx, "is one of")
	case "not_one_of":
		return T(ctx, "is not one of")
	case "matches":
		return T(ctx, "matches regexp")
	case "approx":
		return T(ctx, "is about")
	case "between":
		return T(ctx, "is between")
	case "gt":
		return ">"
	case "gte":
		return "≥"
	case "lt":
		return "<"
	case "lte":
		return "≤"
	default:
		return string(op)
	}
}

// RuleActionLabel returns the translated name of rule action type a.
func RuleActionLabel(ctx context.Context, a domain.ActionType) string {
	switch a {
	case "set_category":
		return T(ctx, "Set category")
	case "set_payee":
		return T(ctx, "Rename payee")
	case "set_note":
		return T(ctx, "Set note")
	case "append_note":
		return T(ctx, "Append to note")
	case "prepend_note":
		return T(ctx, "Prepend to note")
	case "add_tag":
		return T(ctx, "Add tag")
	default:
		return string(a)
	}
}
