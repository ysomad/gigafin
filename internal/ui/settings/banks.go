package settings

import (
	"cmp"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// loadBanks sets the state of the banks card in d.
func (h *Handler) loadBanks(r *http.Request, user domain.User, d *banksData) error {
	ctx := r.Context()
	svc := h.app.Svc.TBank
	if d.TBank.Enabled = svc.Enabled(); d.TBank.Enabled {
		conn, err := svc.Connection(ctx, user.ID)
		switch {
		case err == nil:
			d.TBank.Connected, d.TBank.Conn = true, conn
		case !errors.Is(err, errors.ErrNotFound):
			return err
		}
		var phone string
		d.TBank.Step, phone = svc.LoginStep(user.ID)
		if d.TBank.Phone == "" {
			d.TBank.Phone = cmp.Or(phone, conn.Login)
		}
	}
	var err error
	if d.Accounts, err = h.app.Svc.Banks.Accounts(ctx, user.ID); err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(ctx, user.ID)
	if err != nil {
		return err
	}
	d.Linked = make(map[int64]domain.Account, len(accounts))
	for _, a := range accounts {
		d.Linked[a.ID] = a
	}
	d.Choices, d.Suggested = make(map[int64][]domain.Account), make(map[int64]int64)
	for _, t := range d.Accounts {
		if t.AccountID != 0 {
			continue
		}
		for _, a := range accounts {
			if a.Archived || a.Currency != t.Currency ||
				slices.ContainsFunc(d.Accounts, func(t domain.BankAccount) bool { return t.AccountID == a.ID }) {
				continue
			}
			d.Choices[t.ID] = append(d.Choices[t.ID], a)
			// the account imports of the bank map it to, else one of its name
			if a.ID == t.TransfersTo || (d.Suggested[t.ID] == 0 && a.Name == t.Name) {
				d.Suggested[t.ID] = a.ID
			}
		}
	}
	return nil
}

func (h *Handler) tbankLogin(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	phone := strings.TrimSpace(r.PostFormValue("phone"))
	posted := pageData{Banks: banksData{TBank: tbankData{Phone: phone}}}
	if _, err := domain.NormalizePhone(phone); err != nil {
		errs := kit.FieldsError{"tbank.phone": err}
		return h.settingsReply(w, r, user, _cardBanks, posted, kit.Failure{Err: errs, Form: "tbank"})
	}
	if _, err := h.app.Svc.TBank.StartLogin(r.Context(), user.ID, phone); err != nil {
		return h.settingsReply(w, r, user, _cardBanks, posted, kit.Failure{Err: err, Form: "tbank"})
	}
	return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
}

func (h *Handler) tbankConfirm(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	value := strings.TrimSpace(r.PostFormValue("value"))
	if value == "" {
		errs := kit.FieldsError{"tbank.value": domain.NewError(errors.ErrInvalid, "required")}
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: errs, Form: "tbank"})
	}
	ctx := r.Context()
	step, err := h.app.Svc.TBank.ConfirmLogin(ctx, user.ID, value)
	if err != nil {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: err, Form: "tbank"})
	}
	if step != "" {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
	}
	// signed in: the accounts are read, the linked ones catch up
	res, err := h.app.Svc.TBank.Sync(ctx, user.ID, user.Location(), kit.T(ctx, "Uncategorized"), true /* force */)
	d := pageData{Notice: kit.T(ctx, "T-Bank connected.")}
	switch {
	case err != nil:
		h.app.Log.WarnContext(ctx, "sync t-bank", slog.Any("error", err))
		d.Banks.SyncErr = err.Error()
	case res.Operations > 0:
		d.Notice = tbankNotice(r, res)
	}
	if !kit.IsHTMX(r) {
		h.app.SetNotice(w, d.Notice)
	}
	return h.settingsReply(w, r, user, _cardBanks, d, kit.Failure{})
}

func (h *Handler) tbankCancel(w http.ResponseWriter, r *http.Request, user domain.User) error {
	h.app.Svc.TBank.CancelLogin(user.ID)
	return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
}

func (h *Handler) tbankSync(w http.ResponseWriter, r *http.Request, user domain.User) error {
	ctx := r.Context()
	res, err := h.app.Svc.TBank.Sync(ctx, user.ID, user.Location(), kit.T(ctx, "Uncategorized"), true /* force */)
	var d pageData
	if err != nil {
		// a failing bank is not a fault of this server; the card shows it too
		h.app.Log.WarnContext(ctx, "sync t-bank", slog.Any("error", err))
		d.Banks.SyncErr = err.Error()
	} else {
		d.Notice = tbankNotice(r, res)
	}
	if !kit.IsHTMX(r) && d.Notice != "" {
		h.app.SetNotice(w, d.Notice)
	}
	return h.settingsReply(w, r, user, _cardBanks, d, kit.Failure{})
}

// bankLink syncs the bank account posted as bank_account, or every one not synced yet,
// with the account chosen for it as account_ID, its id: an account of its currency no
// bank account syncs with, or a new one when "new". All sync in one run, so the
// transfers between them pair. A new account imports all operations and starts at the
// balance of the bank; an account with operations imports those after its last day.
func (h *Handler) bankLink(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	ctx, loc := r.Context(), user.Location()
	bankAccounts, err := h.app.Svc.Banks.Accounts(ctx, user.ID)
	if err != nil {
		return err
	}
	picks, err := bankPicks(r, bankAccounts)
	if kit.IsUserError(err) {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: err, Form: "banks"})
	}
	if err != nil {
		return err
	}
	// all operations, as for an account without operations
	all := time.Unix(0, 0).In(loc)
	var (
		links []domain.BankLink
		// fresh are the accounts without operations, by id, set to the bank balance
		fresh = map[int64]domain.BankAccount{}
	)
	for _, link := range picks {
		i := slices.IndexFunc(bankAccounts, func(t domain.BankAccount) bool { return t.ID == link.BankAccountID })
		t := bankAccounts[i]
		link.From = all
		if link.AccountID == 0 {
			in := domain.AccountInput{Name: t.Name, Currency: t.Currency, Kind: t.Kind}
			if !t.CreditLimit.IsZero() {
				in.Kind, in.CreditLimit = domain.AccountCredit, t.CreditLimit
			}
			a, err := h.app.Svc.Accounts.Create(ctx, user.ID, in)
			if kit.IsUserError(err) {
				return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: err, Form: "banks"})
			}
			if err != nil {
				return err
			}
			link.AccountID = a.ID
		} else if link.From, err = h.syncStart(r, user, link.AccountID, all); err != nil {
			return err
		}
		if link.From.Equal(all) {
			fresh[link.AccountID] = t
		}
		links = append(links, link)
	}
	if len(links) == 0 {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
	}
	res, err := h.app.Svc.TBank.Link(ctx, user.ID, links, loc, kit.T(ctx, "Uncategorized"))
	var d pageData
	if err != nil {
		h.app.Log.WarnContext(ctx, "link t-bank accounts", slog.Any("error", err))
		d.Banks.SyncErr = err.Error()
	} else {
		for id, t := range fresh {
			// the opening balance, before the first imported day, is the balance now less
			// the imported operations
			opening := t.Balance.Sub(res.Fetched[id])
			if _, err := h.app.Svc.Accounts.SetBalance(ctx, user.ID, id, opening,
				timex.Date(all).AddDate(0, 0, -1)); err != nil {
				return err
			}
		}
		d.Notice = tbankNotice(r, res)
	}
	if !kit.IsHTMX(r) && d.Notice != "" {
		h.app.SetNotice(w, d.Notice)
	}
	return h.settingsReply(w, r, user, _cardBanks, d, kit.Failure{})
}

// syncStart returns the start of the sync of the account accountID: the day after its
// last operation, or all when it has none.
func (h *Handler) syncStart(r *http.Request, user domain.User, accountID int64, all time.Time) (time.Time, error) {
	ops, _, err := h.app.Svc.Operations.List(r.Context(), user.ID, domain.Filter{AccountID: accountID},
		pagination.Params{Limit: 1})
	if err != nil || len(ops) == 0 {
		return all, err
	}
	last := ops[0].Date
	return time.Date(last.Year(), last.Month(), last.Day()+1, 0, 0, 0, 0, user.Location()), nil
}

// bankPicks returns the links of the T-Bank accounts not synced of the bank link form
// of r, the one posted as bank_account or all, to the accounts chosen for them, 0 for a
// new one, without a start. An account may be chosen once.
func bankPicks(r *http.Request, bankAccounts []domain.BankAccount) ([]domain.BankLink, error) {
	only := r.PostFormValue("bank_account")
	var picks []domain.BankLink
	chosen := make(map[int64]bool)
	for _, t := range bankAccounts {
		id := strconv.FormatInt(t.ID, 10)
		if t.Bank != domain.BankTBank || t.AccountID != 0 || (only != "" && only != id) {
			continue
		}
		var accountID int64
		if choice := r.PostFormValue("account_" + id); choice != "" && choice != "new" {
			var err error
			if accountID, err = strconv.ParseInt(choice, 10, 64); err != nil {
				return nil, domain.NewError(errors.ErrInvalid, "invalid form")
			}
			if chosen[accountID] {
				return nil, domain.NewError(errors.ErrInvalid, "an account can sync with one bank account only")
			}
			chosen[accountID] = true
		}
		picks = append(picks, domain.BankLink{AccountID: accountID, BankAccountID: t.ID})
	}
	return picks, nil
}

func (h *Handler) tbankDisconnect(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := h.app.Svc.TBank.Disconnect(r.Context(), user.ID); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
}

// banksAutoSync syncs the banks when the user opens the app, at most once in the sync
// interval; the layout loads it into the top of a page loaded in full. When a bank asks
// to sign in again it answers with an alert linking to the banks card, which shows it
// already on the settings page; when the sync adds operations the page is loaded again
// to show them.
func (h *Handler) banksAutoSync(w http.ResponseWriter, r *http.Request, user domain.User) error {
	ctx := r.Context()
	res, err := h.app.Svc.TBank.Sync(ctx, user.ID, user.Location(), kit.T(ctx, "Uncategorized"), false /* force */)
	if err != nil {
		h.app.Log.WarnContext(ctx, "sync t-bank", slog.Any("error", err))
	}
	v := kit.Page{User: &user}
	current, urlErr := url.Parse(r.Header.Get("HX-Current-URL"))
	switch {
	case res.NeedsLogin && (urlErr != nil || current.Path != "/settings"):
		return h.app.Fragment(w, r, http.StatusOK, bankLoginAlert())
	case res.Operations > 0:
		h.app.SetNotice(w, tbankNotice(r, res))
		w.Header().Set("HX-Refresh", "true")
		return nil
	case err != nil:
		d := pageData{Banks: banksData{SyncErr: err.Error()}}
		return h.app.Fragment(w, r, http.StatusOK, kit.Toasts(v, true, toasts(d)))
	}
	// an empty answer removes the placeholder
	return nil
}

// tbankNotice describes the result of a sync.
func tbankNotice(r *http.Request, res domain.BankSync) string {
	ctx := r.Context()
	switch {
	case res.NeedsLogin:
		return kit.T(ctx, "T-Bank asks to sign in again to keep syncing.")
	case res.Operations > 0:
		return kit.T(ctx, "T-Bank synced: operations %d.", res.Operations)
	default:
		return kit.T(ctx, "T-Bank is up to date.")
	}
}
