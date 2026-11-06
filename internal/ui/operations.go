package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/timex"
)

type dayGroup struct {
	Date time.Time
	Ops  []domain.Operation
	// Continued is set on a day the previous page ended in, so its title is not repeated.
	Continued bool
}

type opsData struct {
	Days     []dayGroup
	MoreURL  string
	Filter   domain.Filter
	Filtered bool
	// FilterCount is the number of Filter fields set in the filter dialog, all but Query.
	FilterCount int
	Accounts    []domain.Account
	AccByID     map[int64]domain.Account
	Categories  []domain.Category
	CatNames    map[int64]string
	Tags        []string
	// RunURL runs rules on the operations matching Filter.
	RunURL string
	// Changed is the number of operations rules just changed, -1 when they did not run.
	Changed int
}

func groupByDay(ops []domain.Operation) []dayGroup {
	var days []dayGroup
	for _, op := range ops {
		if len(days) == 0 || !days[len(days)-1].Date.Equal(op.Date) {
			days = append(days, dayGroup{Date: op.Date})
		}
		days[len(days)-1].Ops = append(days[len(days)-1].Ops, op)
	}
	return days
}

func (u *UI) filterFromQuery(q url.Values) (domain.Filter, error) {
	var in struct {
		Query    string `form:"q"`
		From     string `form:"from"`
		To       string `form:"to"`
		Account  int64  `form:"account"`  // 0 means all
		Category int64  `form:"category"` // 0 means all
		Type     string `form:"type"`
		Min      string `form:"min"`
		Max      string `form:"max"`
		Tags     string `form:"tags"` // comma-separated
	}
	if err := u.formDecoder.Decode(&in, q); err != nil {
		return domain.Filter{}, fmt.Errorf("filter: %w: %w", err, errors.ErrInvalid)
	}
	f := domain.Filter{Query: in.Query, AccountID: in.Account, CategoryID: in.Category}
	for tag := range strings.SplitSeq(in.Tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			f.Tags = append(f.Tags, tag)
		}
	}
	var err error
	if in.From != "" {
		if f.DateFrom, err = timex.ParseDate(in.From); err != nil {
			return f, fmt.Errorf("from: %w", err)
		}
	}
	if in.To != "" {
		if f.DateTo, err = timex.ParseDate(in.To); err != nil {
			return f, fmt.Errorf("to: %w", err)
		}
	}
	if in.Type != "" {
		if f.Type, err = domain.ParseOpType(in.Type); err != nil {
			return f, err
		}
	}
	if in.Min != "" {
		if f.AmountMin.Amount, err = money.Parse(in.Min); err != nil {
			return f, fmt.Errorf("min: %w", err)
		}
		f.AmountMin.Valid = true
	}
	if in.Max != "" {
		if f.AmountMax.Amount, err = money.Parse(in.Max); err != nil {
			return f, fmt.Errorf("max: %w", err)
		}
		f.AmountMax.Valid = true
	}
	return f, nil
}

func (u *UI) operations(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	f, err := u.filterFromQuery(q)
	if err != nil {
		return err
	}
	var p pagination.Params
	if c := q.Get("cursor"); c != "" {
		cur, err := pagination.ParseCursor(c)
		if err != nil {
			return err
		}
		p.After = cur
	}
	ops, next, err := u.svc.Operations.List(r.Context(), user.ID, f, p)
	if err != nil {
		return err
	}
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	days := groupByDay(ops)
	if len(days) > 0 && !p.After.IsZero() && days[0].Date.Equal(p.After.Date) {
		days[0].Continued = true
	}
	d := opsData{
		Days:       days,
		Filter:     f,
		Filtered:   f.Filtered(),
		Accounts:   accounts,
		AccByID:    make(map[int64]domain.Account, len(accounts)),
		Categories: tree,
		CatNames:   domain.CategoryNames(tree),
		Changed:    -1,
	}
	for _, a := range accounts {
		d.AccByID[a.ID] = a
	}
	for _, set := range []bool{
		f.AccountID != 0, f.CategoryID != 0, f.Type != "", !f.DateFrom.IsZero(), !f.DateTo.IsZero(),
		f.AmountMin.Valid, f.AmountMax.Valid, len(f.Tags) > 0,
	} {
		if set {
			d.FilterCount++
		}
	}
	// negative counts are not shown
	if n, err := strconv.Atoi(q.Get("changed")); err == nil {
		d.Changed = n
	}
	run := r.URL.Query()
	run.Del("cursor")
	run.Del("changed")
	d.RunURL = "/operations/rules?" + run.Encode()
	if !next.IsZero() {
		q.Set("cursor", next.String())
		d.MoreURL = "/operations?" + q.Encode()
	}
	if !p.After.IsZero() && r.Header.Get("HX-Request") == "true" {
		return u.fragment(w, r, http.StatusOK, "op-rows", d)
	}
	if d.Tags, err = u.svc.Operations.Tags(r.Context(), user.ID); err != nil {
		return err
	}
	return u.render(
		w,
		r,
		http.StatusOK,
		"operations",
		view{User: &user, Nav: "operations", Title: "Operations", Data: d},
	)
}

type splitForm struct {
	CategoryID int64
	Amount     string
	Note       string
}

// opForm is decoded from the operation form; untagged fields are view only.
type opForm struct {
	ID int64
	// ImportID and OriginID are posted back only to re-render the form; the service
	// ignores them.
	ImportID    int64         `form:"import_id"`
	OriginID    int64         `form:"origin_id"`
	Type        domain.OpType `form:"type"`
	AccountID   int64         `form:"account"`    // 0 is not found
	ToAccountID int64         `form:"to_account"` // 0 is not found
	CategoryID  int64         `form:"category"`   // 0 is reported by validation
	Amount      string        `form:"amount"`
	Date        string        `form:"date"`
	Time        string        `form:"time"`
	Payee       string        `form:"payee"`
	Note        string        `form:"note"`
	// SourcePayee is the payee as the bank sent it.
	SourcePayee string
	// Tags are comma separated names.
	Tags string `form:"tags"`
	// Parts are the rows of the split page of a saved expense or income.
	Parts      []splitForm
	Accounts   []domain.Account
	Categories []domain.Category
	// AllTags are the user's tags to pick from.
	AllTags []string
}

func (u *UI) fillForm(r *http.Request, user domain.User, f *opForm) error {
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if !a.Archived || a.ID == f.AccountID || a.ID == f.ToAccountID {
			f.Accounts = append(f.Accounts, a)
		}
	}
	if f.AllTags, err = u.svc.Operations.Tags(r.Context(), user.ID); err != nil {
		return err
	}
	if f.Type == domain.OpExpense || f.Type == domain.OpIncome {
		tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
		if err != nil {
			return err
		}
		kind := domain.KindExpense
		if f.Type == domain.OpIncome {
			kind = domain.KindIncome
		}
		f.Categories = domain.ByKind(tree, kind)
	}
	return nil
}

func (u *UI) renderForm(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f opForm, viewErr error,
) error {
	if err := u.fillForm(r, user, &f); err != nil {
		return err
	}
	title := "New " + string(f.Type)
	if f.ID != 0 {
		title = "Edit " + string(f.Type)
	}
	return u.render(w, r, status, "operation_form", view{
		User: &user, Nav: "operations", Title: title, Error: viewErr, Form: "operation", Data: f,
	})
}

func (u *UI) newOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	typ, err := domain.ParseOpType(r.URL.Query().Get("type"))
	if err != nil {
		typ = domain.OpExpense
	}
	now := time.Now().In(user.Location())
	f := opForm{Type: typ, Date: timex.FormatDate(now), Time: now.Format("15:04")}
	return u.renderForm(w, r, user, http.StatusOK, f, nil)
}

func (u *UI) operationSplit(w http.ResponseWriter, r *http.Request, user domain.User) error {
	kind, err := domain.ParseKind(r.URL.Query().Get("type"))
	if err != nil {
		return err
	}
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	return u.fragment(w, r, http.StatusOK, "split", map[string]any{
		"Categories": domain.ByKind(tree, kind), "Split": splitForm{},
	})
}

// parseOpForm reads the form into both the raw form, for re-rendering, and the
// domain input, and returns the messages of its invalid inputs translated to lang.
//
//nolint:cyclop // every input of the form is checked here, so the checks stay together
func (u *UI) parseOpForm(r *http.Request, lang string) (opForm, domain.OperationInput, fieldsError, error) {
	if err := r.ParseForm(); err != nil {
		return opForm{}, domain.OperationInput{}, nil, fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f opForm
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return f, domain.OperationInput{}, nil, fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}

	errs := fieldsError{}
	in := domain.OperationInput{
		Type:        f.Type,
		AccountID:   f.AccountID,
		ToAccountID: f.ToAccountID,
		CategoryID:  f.CategoryID,
		Time:        strings.TrimSpace(f.Time),
		Payee:       strings.TrimSpace(f.Payee),
		Note:        strings.TrimSpace(f.Note),
		Tags:        strings.Split(f.Tags, ","),
	}
	if in.AccountID == 0 {
		errs["account"] = u.dicts.T(lang, "required")
	}
	switch {
	case f.Type == domain.OpTransfer && in.ToAccountID == 0:
		errs["to_account"] = u.dicts.T(lang, "required")
	case f.Type == domain.OpTransfer && in.ToAccountID == in.AccountID:
		errs["to_account"] = u.dicts.T(lang, "must differ from the source account")
	case (f.Type == domain.OpExpense || f.Type == domain.OpIncome) && in.CategoryID == 0:
		errs["category"] = u.dicts.T(lang, "required")
	}
	var err error
	switch in.Amount, err = money.Parse(f.Amount); {
	case strings.TrimSpace(f.Amount) == "":
		errs["amount"] = u.dicts.T(lang, "required")
	case err != nil:
		errs["amount"] = u.dicts.T(lang, "enter a number")
	case f.Type == domain.OpAdjustment && in.Amount.IsZero():
		errs["amount"] = u.dicts.T(lang, "must not be zero")
	case f.Type != domain.OpAdjustment && !in.Amount.IsPositive():
		errs["amount"] = u.dicts.T(lang, "must be positive")
	}
	switch in.Date, err = timex.ParseDate(f.Date); {
	case f.Date == "":
		errs["date"] = u.dicts.T(lang, "required")
	case err != nil:
		errs["date"] = u.dicts.T(lang, "must be YYYY-MM-DD")
	}
	if in.Time != "" {
		if _, err := timex.ParseClock(in.Time); err != nil {
			errs["date"] = u.dicts.T(lang, "time must be HH:MM")
		}
	}
	if utf8.RuneCountInString(in.Payee) > domain.MaxTextLen {
		errs["payee"] = u.dicts.T(lang, "too long")
	}
	if utf8.RuneCountInString(in.Note) > domain.MaxTextLen {
		errs["note"] = u.dicts.T(lang, "too long")
	}
	var tags int
	for _, t := range in.Tags {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		tags++
		if utf8.RuneCountInString(t) > domain.MaxTagLen {
			errs["tags"] = u.dicts.T(lang, "%q: longer than %d characters", t, domain.MaxTagLen)
		}
	}
	if tags > domain.MaxTags {
		errs["tags"] = u.dicts.T(lang, "at most %d", domain.MaxTags)
	}
	return f, in, errs, nil
}

func (u *UI) createOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	f, in, errs, err := u.parseOpForm(r, p.lang)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return u.renderForm(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	_, err = u.svc.Operations.Create(r.Context(), user.ID, in)
	if isUserError(err) {
		return u.renderForm(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/operations")
	return nil
}

func (u *UI) editOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	op, err := u.svc.Operations.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	f := opForm{
		ID:          op.ID,
		ImportID:    op.ImportID,
		OriginID:    op.OriginID,
		Type:        op.Type,
		AccountID:   op.AccountID,
		ToAccountID: op.ToAccountID,
		CategoryID:  op.CategoryID,
		Amount:      op.Amount.Fixed(op.Currency),
		Date:        timex.FormatDate(op.Date),
		Time:        op.Time,
		Payee:       op.Payee,
		Note:        op.Note,
		Tags:        strings.Join(op.Tags, ", "),
		SourcePayee: op.SourcePayee,
	}
	return u.renderForm(w, r, user, http.StatusOK, f, nil)
}

func (u *UI) splitOperationPage(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	return u.renderSplit(w, r, user, id, http.StatusOK, nil, nil)
}

// renderSplit renders the split page of expense or income id with parts and splitErr,
// or the operation as the first part when parts is empty.
func (u *UI) renderSplit(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, parts []splitForm, splitErr error,
) error {
	op, err := u.svc.Operations.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	kind := domain.KindExpense
	switch op.Type {
	case domain.OpExpense:
	case domain.OpIncome:
		kind = domain.KindIncome
	default:
		return errors.ErrNotFound
	}
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	amount := op.Amount.Fixed(op.Currency)
	if len(parts) == 0 {
		parts = []splitForm{{CategoryID: op.CategoryID, Amount: amount}, {}}
	}
	return u.render(w, r, status, "operation_split", view{
		User: &user, Nav: "operations", Title: "Split", Error: splitErr, Form: "split", Data: opForm{
			ID:         op.ID,
			OriginID:   op.OriginID,
			Type:       op.Type,
			Amount:     amount,
			Payee:      op.Payee,
			Parts:      parts,
			Categories: domain.ByKind(tree, kind),
		},
	})
}

func (u *UI) splitOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	// part rows are added client-side, so they come as parallel lists
	var in struct {
		Categories []int64  `form:"split_category"` // 0 is reported by validation
		Amounts    []string `form:"split_amount"`
		Notes      []string `form:"split_note"`
	}
	if err := u.formDecoder.Decode(&in, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if len(in.Amounts) != len(in.Categories) || len(in.Notes) != len(in.Categories) {
		return domain.NewError(errors.ErrInvalid, "split parts do not match")
	}
	var rows []splitForm
	for i, cat := range in.Categories {
		row := splitForm{CategoryID: cat, Amount: in.Amounts[i], Note: in.Notes[i]}
		if row.CategoryID == 0 && row.Amount == "" && row.Note == "" {
			continue
		}
		rows = append(rows, row)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	parts := make([]domain.SplitPart, len(rows))
	for i, row := range rows {
		// keys index the rows as re-rendered, which drops the empty ones
		n := "[" + strconv.Itoa(i) + "]"
		if row.CategoryID == 0 {
			errs["split_category"+n] = u.dicts.T(p.lang, "required")
		}
		switch d, err := money.Parse(row.Amount); {
		case err != nil:
			errs["split_amount"+n] = u.dicts.T(p.lang, "enter a number")
		case !d.IsPositive():
			errs["split_amount"+n] = u.dicts.T(p.lang, "must be positive")
		default:
			parts[i].Amount = d
		}
		if utf8.RuneCountInString(strings.TrimSpace(row.Note)) > domain.MaxTextLen {
			errs["split_note"+n] = u.dicts.T(p.lang, "too long")
		}
		parts[i].CategoryID, parts[i].Note = row.CategoryID, row.Note
	}
	if len(errs) > 0 {
		return u.renderSplit(w, r, user, id, http.StatusUnprocessableEntity, rows, errs)
	}
	_, err = u.svc.Operations.Split(r.Context(), user.ID, id, parts)
	if isUserError(err) {
		return u.renderSplit(w, r, user, id, http.StatusUnprocessableEntity, rows, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/operations")
	return nil
}

func (u *UI) updateOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	f, in, errs, err := u.parseOpForm(r, p.lang)
	if err != nil {
		return err
	}
	f.ID = id
	if len(errs) > 0 {
		return u.renderForm(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	_, err = u.svc.Operations.Update(r.Context(), user.ID, id, in)
	if isUserError(err) {
		return u.renderForm(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/operations")
	return nil
}

func (u *UI) deleteOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Operations.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/operations")
	return nil
}
