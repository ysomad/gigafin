// Package kit is shared by the UI features: the app the handlers run in, the data
// of every page, the funcs templates call and the components of more than one page.
package kit

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/i18n"
)

// Page is the data of every page, besides the data of the page itself.
type Page struct {
	User *domain.User
	Nav  string
	// Title is translated, then formatted with TitleArgs, if any.
	Title     string
	TitleArgs []any
	// Errors are set by the renderer from the Failure of a form post or a flash message.
	Errors *FormErrors
	// Notice is a success message set before a redirect, shown as a toast.
	Notice string
	// BankSync is set by the renderer when the user opens the app, a signed-in page
	// loaded in full rather than by htmx, and the sync of a bank is on: the page then
	// syncs the banks.
	BankSync bool
}

// Failure is a failed form post a page is rendered for: Err is FieldsError of its
// inputs, or an error of the business logic, shown on the form named Form if set, else
// as a toast. The zero Failure is none.
type Failure struct {
	Err  error
	Form string
}

// FieldsError holds the untranslated errors of the inputs of a form that failed
// validation, keyed by input name; the renderer translates them. A handler validates
// every input of its form, before calling a service, and passes them as the Failure.
type FieldsError map[string]error

// type check
var _ error = FieldsError{}

// Error implements the error interface for FieldsError.
func (e FieldsError) Error() string {
	msgs := make([]string, 0, len(e))
	for _, k := range slices.Sorted(maps.Keys(e)) {
		msgs = append(msgs, k+": "+e[k].Error())
	}
	return strings.Join(msgs, "; ")
}

// FormErrors are the translated messages of an error shown on a page: field messages,
// shown next to their inputs, a form message, shown on the posted form, and a toast.
type FormErrors struct {
	Fields map[string]string
	// FormName is the posted form, which shows Msg.
	FormName string
	Msg      string
	Toast    string
}

// Get returns the message about the input named field, if any.
func (e *FormErrors) Get(field string) string {
	if e == nil {
		return ""
	}
	return e.Fields[field]
}

// Form returns the message about the form named name, if any.
func (e *FormErrors) Form(name string) string {
	if e == nil || e.FormName != name {
		return ""
	}
	return e.Msg
}

// Toasts returns the messages shown as toasts.
func (e *FormErrors) Toasts() []string {
	if e == nil || e.Toast == "" {
		return nil
	}
	return []string{e.Toast}
}

// Prefs are the settings and UI language of the signed-in user, read once per request
// and kept in the request context, with the dicts to translate to the language. A
// handler that changes the settings redirects, so they are never stale when rendered,
// except the settings page rendered for htmx, which reads the settings itself and shows
// no amounts.
type Prefs struct {
	Settings domain.Settings
	Lang     string
	Dicts    i18n.Dicts
	// Today is the current date in the user's time zone, zero when signed out.
	Today time.Time
}

type prefsKey struct{}

// WithPrefs returns ctx holding p.
func WithPrefs(ctx context.Context, p Prefs) context.Context {
	return context.WithValue(ctx, prefsKey{}, p)
}

// LookupPrefs returns the prefs of ctx and whether it holds any.
func LookupPrefs(ctx context.Context) (Prefs, bool) {
	p, ok := ctx.Value(prefsKey{}).(Prefs)
	return p, ok
}

// PrefsFrom returns the prefs of ctx, zero if it holds none.
func PrefsFrom(ctx context.Context) Prefs {
	p, _ := LookupPrefs(ctx)
	return p
}
