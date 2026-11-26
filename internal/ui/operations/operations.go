package operations

import (
	"cmp"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the operations pages.
type Handler struct{ app *kit.App }

// New returns the handler of the operations pages.
func New(app *kit.App) *Handler { return &Handler{app: app} }

// Routes returns the routes of the operations pages.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /operations":               h.operations,
		"GET /operations/new":           h.newOperation,
		"GET /operations/split":         h.operationSplit,
		"POST /operations":              h.createOperation,
		"POST /operations/fill":         h.fillOperation,
		"POST /operations/rules":        h.categorizeOperations,
		"POST /operations/sync":         h.syncBanks,
		"GET /operations/{id}/edit":     h.editOperation,
		"POST /operations/{id}":         h.updateOperation,
		"POST /operations/{id}/delete":  h.deleteOperation,
		"GET /operations/{id}/split":    h.splitOperationPage,
		"POST /operations/{id}/split":   h.splitOperation,
		"POST /operations/{id}/unsplit": h.unsplitOperation,
	}
}

func (h *Handler) operations(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	f, err := h.app.FilterFromQuery(q)
	if err != nil {
		return err
	}
	var after pagination.Cursor
	if c := q.Get("cursor"); c != "" {
		if after, err = pagination.ParseCursor(c); err != nil {
			return domain.NewError(errors.ErrInvalid, "invalid page link")
		}
	}
	p := pagination.NewParams(after, 0)
	ops, next, err := h.app.Svc.Operations.List(r.Context(), user.ID, f, p)
	if err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	days := kit.GroupByDay(ops)
	if f.Sort.ByAmount() && len(ops) > 0 {
		// an order by amount runs days apart, so the rows are listed with their dates
		days = []kit.DayGroup{{Ops: ops}}
	}
	if len(days) > 0 && !p.After.IsZero() && days[0].Date.Equal(p.After.Date) {
		days[0].Continued = true
	}
	if err := h.app.WholeLastDay(r.Context(), user.ID, f, days, next); err != nil {
		return err
	}
	d := opList{
		Days:        days,
		Filter:      f,
		Filtered:    f.Filtered(),
		FilterCount: f.Count(),
		Accounts:    accounts,
		AccByID:     domain.AccountsByID(accounts),
		Categories:  tree,
		CatNames:    domain.CategoryShortNames(tree),
		Changed:     -1,
	}
	// negative counts are not shown
	if n, err := strconv.Atoi(q.Get("changed")); err == nil {
		d.Changed = n
	}
	run := r.URL.Query()
	run.Del("cursor")
	run.Del("changed")
	d.RunURL = "/operations/rules?" + run.Encode()
	d.ResetURL = "/operations"
	if f.Sort != "" {
		d.ResetURL += "?" + url.Values{"sort": {string(f.Sort)}}.Encode()
	}
	if !next.IsZero() {
		q.Set("cursor", next.String())
		d.MoreURL = "/operations?" + q.Encode()
	}
	if !p.After.IsZero() && kit.IsHTMX(r) {
		return h.app.Fragment(w, r, http.StatusOK, kit.OpRows(d.Days, d.CatNames, d.AccByID, d.MoreURL))
	}
	if d.Tags, err = h.app.Svc.Operations.Tags(r.Context(), user.ID); err != nil {
		return err
	}
	if err := h.loadBanks(r, user, &d); err != nil {
		return err
	}
	return kit.Render(
		h.app,
		w,
		r,
		http.StatusOK,
		listPage,
		kit.Page{User: &user, Nav: "operations", Title: "Operations"},
		d,
		kit.Failure{},
	)
}

// loadBanks sets in d whether the user syncs a bank and the bank accounts whose
// balance differs from their accounts in d.AccByID.
func (h *Handler) loadBanks(r *http.Request, user domain.User, d *opList) error {
	ctx := r.Context()
	conns, err := h.app.Svc.Banks.Connections(ctx, user.ID)
	if err != nil {
		return err
	}
	enabled := h.app.Svc.Banks.Enabled()
	for _, conn := range conns {
		if !slices.Contains(enabled, conn.Bank) {
			continue
		}
		if conn.Status == domain.BankConnected {
			d.BankSync = true
		}
	}
	bankAccounts, err := h.app.Svc.Banks.Accounts(ctx, user.ID)
	if err != nil {
		return err
	}
	for _, t := range bankAccounts {
		// a failed sync leaves the synced period as it was, so it dates the last successful one
		if slices.Contains(enabled, t.Bank) && t.SyncedUntil.After(d.BankSyncAt) {
			d.BankSyncAt = t.SyncedUntil
		}
		if t.Mismatch(d.AccByID[t.AccountID]) {
			d.Mismatched = append(d.Mismatched, t)
		}
	}
	return nil
}

// syncBanks syncs the banks now and goes back to the operations with the outcome.
func (h *Handler) syncBanks(w http.ResponseWriter, r *http.Request, user domain.User) error {
	ctx := r.Context()
	res, err := h.app.Svc.Banks.SyncAll(ctx, user.ID, user.Location(), true /* force */)
	if err != nil {
		// a failing bank is not a fault of this server; the alert names it
		h.app.Log.WarnContext(ctx, "sync banks", slog.Any("error", err))
		h.app.SetAlert(w, kit.SyncNotice(ctx, res))
	} else {
		h.app.SetNotice(w, kit.SyncNotice(ctx, res))
	}
	kit.Redirect(w, r, "/operations")
	return nil
}

func (h *Handler) fillForm(r *http.Request, user domain.User, f *opForm) error {
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if !a.Archived || a.ID == f.AccountID || a.ID == f.ToAccountID {
			f.Accounts = append(f.Accounts, a)
		}
	}
	if f.AllTags, err = h.app.Svc.Operations.Tags(r.Context(), user.ID); err != nil {
		return err
	}
	if kind, ok := f.Type.Kind(); ok {
		tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
		if err != nil {
			return err
		}
		f.Categories = domain.ByKind(tree, kind)
	}
	return nil
}

func (h *Handler) renderForm(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f opForm, viewErr error,
) error {
	if err := h.fillForm(r, user, &f); err != nil {
		return err
	}
	title := "New " + string(f.Type)
	if f.ID != 0 {
		title = "Edit " + string(f.Type)
	}
	return kit.Render(h.app, w, r, status, formPage, kit.Page{User: &user, Nav: "operations", Title: title}, f,
		kit.Failure{Err: viewErr, Form: "operation"})
}

func (h *Handler) newOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	typ, err := domain.ParseOpType(r.URL.Query().Get("type"))
	if err != nil {
		typ = domain.OpExpense
	}
	now := time.Now().In(user.Location())
	f := opForm{Type: typ, Date: timex.FormatDate(now), Time: now.Format("15:04")}
	return h.renderForm(w, r, user, http.StatusOK, f, nil)
}

func (h *Handler) operationSplit(w http.ResponseWriter, r *http.Request, user domain.User) error {
	kind, err := domain.ParseKind(r.URL.Query().Get("type"))
	if err != nil {
		return err
	}
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	return h.app.Fragment(w, r, http.StatusOK, splitRow(domain.ByKind(tree, kind), splitPart{}, false, 0, nil))
}

// parseOpForm reads the form into both the raw form, for re-rendering, and the
// domain input, and returns the errors of its invalid inputs.
//
//nolint:cyclop // every input of the form is checked here, so the checks stay together
func (h *Handler) parseOpForm(r *http.Request) (opForm, domain.OperationInput, kit.FieldsError, error) {
	var f opForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return f, domain.OperationInput{}, nil, err
	}

	errs := kit.FieldsError{}
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
		errs["account"] = domain.NewError(errors.ErrInvalid, "required")
	}
	switch {
	case f.Type == domain.OpTransfer && in.ToAccountID == 0:
		errs["to_account"] = domain.NewError(errors.ErrInvalid, "required")
	case f.Type == domain.OpTransfer && in.ToAccountID == in.AccountID:
		errs["to_account"] = domain.NewError(errors.ErrInvalid, "must differ from the source account")
	}
	var err error
	switch in.Amount, err = money.Parse(f.Amount); {
	case strings.TrimSpace(f.Amount) == "":
		errs["amount"] = domain.NewError(errors.ErrInvalid, "required")
	case err != nil:
		errs["amount"] = domain.NewError(errors.ErrInvalid, "enter a number")
	case f.Type == domain.OpAdjustment && in.Amount.IsZero():
		errs["amount"] = domain.NewError(errors.ErrInvalid, "must not be zero")
	case f.Type != domain.OpAdjustment && !in.Amount.IsPositive():
		errs["amount"] = domain.NewError(errors.ErrInvalid, "must be positive")
	}
	switch in.Date, err = timex.ParseDate(f.Date); {
	case f.Date == "":
		errs["date"] = domain.NewError(errors.ErrInvalid, "required")
	case err != nil:
		errs["date"] = domain.NewError(errors.ErrInvalid, "must be YYYY-MM-DD")
	}
	if in.Time != "" {
		if _, err := timex.ParseClock(in.Time); err != nil {
			errs["date"] = domain.NewError(errors.ErrInvalid, "time must be HH:MM")
		}
	}
	if utf8.RuneCountInString(in.Payee) > domain.MaxTextLen {
		errs["payee"] = domain.NewError(errors.ErrInvalid, "too long")
	}
	if utf8.RuneCountInString(in.Note) > domain.MaxTextLen {
		errs["note"] = domain.NewError(errors.ErrInvalid, "too long")
	}
	var tags int
	for _, t := range in.Tags {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		tags++
		if utf8.RuneCountInString(t) > domain.MaxTagLen {
			errs["tags"] = domain.NewError(errors.ErrInvalid, "%q: longer than %d characters", t, domain.MaxTagLen)
		}
	}
	if tags > domain.MaxTags {
		errs["tags"] = domain.NewError(errors.ErrInvalid, "at most %d", domain.MaxTags)
	}
	return f, in, errs, nil
}

func (h *Handler) createOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	f, in, errs, err := h.parseOpForm(r)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return h.renderForm(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	_, err = h.app.Svc.Operations.Create(r.Context(), user.ID, in)
	if kit.IsUserError(err) {
		return h.renderForm(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/operations")
	return nil
}

func (h *Handler) editOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	op, err := h.app.Svc.Operations.Get(r.Context(), user.ID, id)
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
		Back:        "/operations",
	}
	// the list the edit was opened from, not a page of the operation itself, such as its
	// split
	if ref, err := url.Parse(r.Referer()); err == nil && ref.Host == r.Host &&
		!strings.HasPrefix(ref.Path+"/", "/operations/"+r.PathValue("id")+"/") {
		f.Back = ref.RequestURI()
	}
	return h.renderForm(w, r, user, http.StatusOK, f, nil)
}

func (h *Handler) splitOperationPage(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	return h.renderSplit(w, r, user, id, http.StatusOK, nil, nil)
}

// renderSplit renders the split page of expense or income id with parts and splitErr,
// or the operation as the first part when parts is empty.
func (h *Handler) renderSplit(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, parts []splitPart, splitErr error,
) error {
	op, err := h.app.Svc.Operations.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	kind, ok := op.Type.Kind()
	if !ok {
		return errors.ErrNotFound
	}
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	amount := op.Amount.Fixed(op.Currency)
	if len(parts) == 0 {
		parts = []splitPart{{CategoryID: op.CategoryID, Amount: amount}, {}}
	}
	return kit.Render(h.app, w, r, status, splitPage, kit.Page{User: &user, Nav: "operations", Title: "Split"},
		opSplit{
			Op:         op,
			CatNames:   domain.CategoryShortNames(tree),
			AccByID:    domain.AccountsByID(accounts),
			Amount:     amount,
			Parts:      parts,
			Categories: domain.ByKind(tree, kind),
		}, kit.Failure{Err: splitErr, Form: "split"})
}

func (h *Handler) splitOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	// part rows are added client-side, so they come as parallel lists
	var in struct {
		Categories []int64  `form:"split_category"` // 0 is uncategorized
		Amounts    []string `form:"split_amount"`
		Notes      []string `form:"split_note"`
	}
	if err := h.app.DecodeForm(r, &in); err != nil {
		return err
	}
	if len(in.Amounts) != len(in.Categories) || len(in.Notes) != len(in.Categories) {
		return domain.NewError(errors.ErrInvalid, "split parts do not match")
	}
	var rows []splitPart
	for i, cat := range in.Categories {
		row := splitPart{CategoryID: cat, Amount: in.Amounts[i], Note: in.Notes[i]}
		if row.CategoryID == 0 && row.Amount == "" && row.Note == "" {
			continue
		}
		rows = append(rows, row)
	}
	errs := kit.FieldsError{}
	parts := make([]domain.SplitPart, len(rows))
	for i, row := range rows {
		// keys index the rows as re-rendered, which drops the empty ones
		n := "[" + strconv.Itoa(i) + "]"
		switch d, err := money.Parse(row.Amount); {
		case err != nil:
			errs["split_amount"+n] = domain.NewError(errors.ErrInvalid, "enter a number")
		case !d.IsPositive():
			errs["split_amount"+n] = domain.NewError(errors.ErrInvalid, "must be positive")
		default:
			parts[i].Amount = d
		}
		if utf8.RuneCountInString(strings.TrimSpace(row.Note)) > domain.MaxTextLen {
			errs["split_note"+n] = domain.NewError(errors.ErrInvalid, "too long")
		}
		parts[i].CategoryID, parts[i].Note = row.CategoryID, row.Note
	}
	if len(errs) > 0 {
		return h.renderSplit(w, r, user, id, http.StatusUnprocessableEntity, rows, errs)
	}
	_, err = h.app.Svc.Operations.Split(r.Context(), user.ID, id, parts)
	if kit.IsUserError(err) {
		return h.renderSplit(w, r, user, id, http.StatusUnprocessableEntity, rows, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/operations")
	return nil
}

func (h *Handler) updateOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	f, in, errs, err := h.parseOpForm(r)
	if err != nil {
		return err
	}
	f.ID = id
	// a new operation may be left uncategorized, a saved one is not
	if (f.Type == domain.OpExpense || f.Type == domain.OpIncome) && in.CategoryID == 0 {
		errs["category"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if len(errs) > 0 {
		return h.renderForm(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	_, err = h.app.Svc.Operations.Update(r.Context(), user.ID, id, in)
	if kit.IsUserError(err) {
		return h.renderForm(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, cmp.Or(f.Back, "/operations"))
	return nil
}

func (h *Handler) deleteOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err := h.app.Svc.Operations.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	kit.Redirect(w, r, cmp.Or(r.PostFormValue("back"), "/operations"))
	return nil
}

// unsplitOperation merges the split operation id is a part of and opens the merged
// operation.
func (h *Handler) unsplitOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	op, err := h.app.Svc.Operations.Unsplit(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	kit.Redirect(w, r, fmt.Sprintf("/operations/%d/edit", op.ID))
	return nil
}

// categorizeOperations runs rules on the operations matching the filter in the query
// and returns to them with the number changed.
func (h *Handler) categorizeOperations(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	f, err := h.app.FilterFromQuery(q)
	if err != nil {
		return err
	}
	n, err := h.app.Svc.Operations.Categorize(r.Context(), user.ID, f)
	if err != nil {
		return err
	}
	q.Del("cursor")
	q.Set("changed", strconv.Itoa(n))
	kit.Redirect(w, r, "/operations?"+q.Encode())
	return nil
}

// fillOperation re-renders the new operation form with the empty fields rules fill.
func (h *Handler) fillOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	// the form is being filled in, so its invalid inputs are no error yet
	f, in, _, err := h.parseOpForm(r) //nolint:errcheck // input messages are not shown here
	if err != nil {
		return err
	}
	filled, err := h.app.Svc.Operations.Fill(r.Context(), user.ID, in)
	if err != nil {
		return err
	}
	f.Payee, f.Note, f.CategoryID = filled.Payee, filled.Note, filled.CategoryID
	var tags []string
	for _, t := range filled.Tags {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	f.Tags = strings.Join(tags, ", ")
	return h.renderForm(w, r, user, http.StatusOK, f, nil)
}
