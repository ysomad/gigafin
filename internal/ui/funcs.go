package ui

import (
	"html/template"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

var _funcs = template.FuncMap{
	"dict":    dict,
	"opClass": opClass,
	"date":    timex.FormatDate,
	"opTypes": domain.OpTypes,
	"kinds":   domain.Kinds,
	"byKind":  domain.ByKind,
	"min":     func(a, b int64) int64 { return min(a, b) },

	"ruleStages":      domain.RuleStages,
	"ruleActionTypes": domain.ActionTypes,
}

// amountFuncs returns the funcs that display amounts, with or without minor units by
// the user's settings. Templates are parsed with defaults and get these per request.
func amountFuncs(s domain.Settings) template.FuncMap {
	format := func(d money.Amount, currency string) string {
		if s.ShowMinorUnits {
			return d.Format(currency)
		}

		return d.FormatWhole()
	}

	return template.FuncMap{
		"money":          format,
		"opAmount":       func(op domain.Operation) string { return opAmount(op, format) },
		"showMinorUnits": func() bool { return s.ShowMinorUnits },
		// amountInput drops zero minor units from an amount input value; it keeps
		// non-zero ones, as saving a rounded value would change the amount
		"amountInput": func(v string) string {
			if s.ShowMinorUnits {
				return v
			}
			if d, err := money.Parse(v); err == nil && d.IsWhole() {
				return d.String()
			}
			return v
		},
	}
}

// langFuncs returns the funcs that translate to lang by d. Templates are parsed with
// English and get these per request.
func langFuncs(d i18n.Dicts, lang string) template.FuncMap {
	t := func(key string, args ...any) string {
		return d.T(lang, key, args...)
	}

	return template.FuncMap{
		"t":       t,
		"lang":    func() string { return lang },
		"errText": func(err error) string { return errText(d, lang, err) },
		"dayTitle": func(tm time.Time) string {
			return t(
				tm.Format("Mon"),
			) + tm.Format(
				", 2 ",
			) + t(
				tm.Format("Jan")+i18n.ContextSep+"date",
			) + tm.Format(
				" 2006",
			)
		},
		"datetime": func(tm time.Time, user *domain.User) string {
			if user != nil {
				tm = tm.In(user.Location())
			}
			return tm.Format("2 ") + t(tm.Format("Jan")+i18n.ContextSep+"date") + tm.Format(" 2006 15:04")
		},
		"opTitle": func(op domain.Operation, names map[int64]string) string { return opTitle(op, names, t) },
		"opSubtitle": func(op domain.Operation, names map[int64]string, accounts map[int64]domain.Account) string {
			return opSubtitle(op, names, accounts, t)
		},
	}
}

// errText returns the message of err translated to lang by d: the field, format and
// domain.Msg args of domain.Error translated, the error kinds ending them dropped,
// other errors as is.
func errText(d i18n.Dicts, lang string, err error) string {
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
	if e.Field != "" {
		msg = d.T(lang, e.Field+i18n.ContextSep+"field") + ": " + msg
	}

	if _, ok := e.Cause.(errors.Error); ok {
		return msg
	}

	return msg + ": " + errText(d, lang, e.Cause)
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.Error("dict: odd number of arguments")
	}

	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, errors.Error("dict: key must be a string")
		}
		m[k] = kv[i+1]
	}

	return m, nil
}

func opTitle(op domain.Operation, names map[int64]string, t func(string, ...any) string) string {
	switch {
	case op.Payee != "":
		return op.Payee
	case op.Type == domain.OpTransfer:
		return t("Transfer")
	case op.Type == domain.OpAdjustment:
		return t("Balance adjustment")
	default:
		return names[op.CategoryID]
	}
}

func opSubtitle(
	op domain.Operation, names map[int64]string, accounts map[int64]domain.Account, t func(string, ...any) string,
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
			parts = append(parts, t("split"))
		}
		parts = append(parts, acc)
	}
	if op.Note != "" {
		parts = append(parts, op.Note)
	}
	return strings.Join(parts, " · ")
}

func opClass(op domain.Operation) string {
	switch op.Type {
	case domain.OpExpense:
		return "expense"
	case domain.OpIncome:
		return "income"
	case domain.OpAdjustment:
		if op.Amount.IsNegative() {
			return "expense"
		}
		return "income"
	default:
		return ""
	}
}

func opAmount(op domain.Operation, format func(money.Amount, string) string) string {
	d := op.Amount
	switch op.Type {
	case domain.OpExpense:
		d = d.Neg()
	case domain.OpIncome, domain.OpAdjustment, domain.OpTransfer:
	}
	s := format(d, op.Currency)
	if d.IsPositive() && op.Type != domain.OpTransfer {
		s = "+" + s
	}
	return s + " " + op.Currency
}
