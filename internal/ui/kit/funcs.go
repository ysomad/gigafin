package kit

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

// DayTitle formats tm as a day title: "Today" or "Yesterday" by the user's date, else
// such as "2 Jan 2006, Mon", without the year when it is the current one.
func DayTitle(ctx context.Context, tm time.Time) string {
	today := PrefsFrom(ctx).Today
	switch {
	case today.IsZero():
	case tm.Equal(today):
		return T(ctx, "Today")
	case tm.Equal(today.AddDate(0, 0, -1)):
		return T(ctx, "Yesterday")
	}
	s := tm.Format("2 ") + T(ctx, tm.Format("Jan")+i18n.ContextSep+"date")
	if !IsCurrentYear(ctx, tm) {
		s += tm.Format(" 2006")
	}
	return s + ", " + T(ctx, tm.Format("Mon"))
}

// Datetime formats tm in the time zone of user, if any, without the year when it is
// the current one.
func Datetime(ctx context.Context, tm time.Time, user *domain.User) string {
	if user != nil {
		tm = tm.In(user.Location())
	}
	s := tm.Format("2 ") + T(ctx, tm.Format("Jan")+i18n.ContextSep+"date")
	if !IsCurrentYear(ctx, tm) {
		s += tm.Format(" 2006")
	}
	return s + tm.Format(" 15:04")
}

// IsCurrentYear reports whether tm is in the current year of the user.
func IsCurrentYear(ctx context.Context, tm time.Time) bool {
	return tm.Year() == PrefsFrom(ctx).Today.Year()
}

// MonthName returns the translated full name of the month of tm and its year, if it is
// not the current one.
func MonthName(ctx context.Context, tm time.Time) string {
	if IsCurrentYear(ctx, tm) {
		return T(ctx, tm.Format("January"))
	}
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

// ListGet returns the attributes of a field of the filter form of a list getting
// action: a change of it swaps in the results, list-results, and the parts of the page
// depending on them, list-reset and the elements of oob, and replaces the URL.
func ListGet(action string, oob ...string) templ.Attributes {
	return templ.Attributes{
		"hx-get":         action,
		"hx-include":     "#filter-form, [form=filter-form]",
		"hx-target":      "#list-results",
		"hx-select":      "#list-results",
		"hx-select-oob":  strings.Join(append([]string{"#list-reset"}, oob...), ", "),
		"hx-swap":        "outerHTML",
		"hx-replace-url": "true",
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

// OpCategory is the first line of op in a list: its category, or its kind for a
// transfer or a balance adjustment.
func OpCategory(ctx context.Context, op domain.Operation, names map[int64]string) string {
	switch {
	case op.Type == domain.OpTransfer:
		return T(ctx, "Transfer")
	case op.Type == domain.OpAdjustment:
		return T(ctx, "Balance adjustment")
	case op.CategoryID == 0:
		return T(ctx, "No category")
	default:
		return names[op.CategoryID]
	}
}

// OpAccounts is the account of op in a list, both accounts of a transfer.
func OpAccounts(op domain.Operation, accounts map[int64]domain.Account) string {
	acc := accounts[op.AccountID].Name
	if op.Type == domain.OpTransfer {
		return acc + " → " + accounts[op.ToAccountID].Name
	}
	return acc
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

// SyncNotice describes the outcome of a bank sync.
func SyncNotice(ctx context.Context, res domain.BankSync) string {
	switch {
	case len(res.NeedsLogin) > 0:
		names := make([]string, len(res.NeedsLogin))
		for i, b := range res.NeedsLogin {
			names[i] = T(ctx, b.Name())
		}
		return T(ctx, "%s asks to sign in again to keep syncing", strings.Join(names, ", "))
	case res.Operations > 0:
		return T(ctx, "Synced operations: %d", res.Operations)
	default:
		return T(ctx, "No new operations")
	}
}
