package settings

import (
	"cmp"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// loadBanks sets the state of the banks card in d.
func (h *Handler) loadBanks(r *http.Request, user domain.User, d *banksData) error {
	ctx := r.Context()
	svc := h.app.Svc.Banks
	conns, err := svc.Connections(ctx, user.ID)
	if err != nil {
		return err
	}
	for _, bank := range svc.Enabled() {
		b := bankData{Bank: bank}
		if i := slices.IndexFunc(conns, func(c domain.BankConnection) bool { return c.Bank == bank }); i >= 0 {
			b.Conn = conns[i]
		}
		var phone string
		b.Step, phone = svc.LoginStep(user.ID, bank)
		b.Phone = cmp.Or(phone, b.Conn.Login)
		if d.Posted.Bank == bank {
			b.Phone = d.Posted.Phone
		}
		d.Banks = append(d.Banks, b)
	}
	if d.Accounts, err = svc.Accounts(ctx, user.ID); err != nil {
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

// pathBank returns the enabled bank of the path of r, or errors.ErrNotFound.
func (h *Handler) pathBank(r *http.Request) (domain.Bank, error) {
	bank := domain.Bank(r.PathValue("bank"))
	if !slices.Contains(h.app.Svc.Banks.Enabled(), bank) {
		return "", errors.ErrNotFound
	}
	return bank, nil
}

func (h *Handler) bankLogin(w http.ResponseWriter, r *http.Request, user domain.User) error {
	bank, err := h.pathBank(r)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	posted := pageData{Banks: banksData{Posted: bankData{Bank: bank, Phone: strings.TrimSpace(r.PostFormValue("phone"))}}}
	phone, err := domain.NormalizePhone(posted.Banks.Posted.Phone)
	if err != nil {
		errs := kit.FieldsError{string(bank) + ".phone": err}
		return h.settingsReply(w, r, user, _cardBanks, posted, kit.Failure{Err: errs, Form: string(bank)})
	}
	if _, err := h.app.Svc.Banks.StartLogin(r.Context(), user.ID, bank, phone); err != nil {
		return h.settingsReply(w, r, user, _cardBanks, posted, kit.Failure{Err: err, Form: string(bank)})
	}
	return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
}

func (h *Handler) bankConfirm(w http.ResponseWriter, r *http.Request, user domain.User) error {
	bank, err := h.pathBank(r)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	value := strings.TrimSpace(r.PostFormValue("value"))
	if value == "" {
		errs := kit.FieldsError{string(bank) + ".value": domain.NewError(errors.ErrInvalid, "required")}
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: errs, Form: string(bank)})
	}
	ctx := r.Context()
	step, err := h.app.Svc.Banks.ConfirmLogin(ctx, user.ID, bank, value)
	if err != nil {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: err, Form: string(bank)})
	}
	if step != "" {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
	}
	// signed in: the accounts are read, the linked ones catch up
	res, err := h.app.Svc.Banks.Sync(ctx, user.ID, bank, user.Location(), true /* force */)
	d := pageData{Notice: kit.T(ctx, "%s connected.", kit.T(ctx, bank.Name()))}
	if res.Operations > 0 {
		d.Notice = kit.SyncNotice(ctx, res)
	}
	return h.syncReply(w, r, user, d, err)
}

func (h *Handler) bankCancel(w http.ResponseWriter, r *http.Request, user domain.User) error {
	bank, err := h.pathBank(r)
	if err != nil {
		return err
	}
	h.app.Svc.Banks.CancelLogin(user.ID, bank)
	return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{})
}

// bankLink syncs every bank account at the bank not synced yet with the account chosen
// for it as account_ID, its id: an account of its currency no bank account syncs with,
// or a new one when "new", all in one run, so the transfers between them pair; then
// syncs the accounts synced already.
func (h *Handler) bankLink(w http.ResponseWriter, r *http.Request, user domain.User) error {
	bank, err := h.pathBank(r)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	ctx := r.Context()
	bankAccounts, err := h.app.Svc.Banks.Accounts(ctx, user.ID)
	if err != nil {
		return err
	}
	links, err := bankPicks(r, bank, bankAccounts)
	var res domain.BankSync
	if err == nil && len(links) > 0 {
		res, err = h.app.Svc.Banks.Link(ctx, user.ID, bank, links, user.Location())
	}
	if kit.IsUserError(err) {
		return h.settingsReply(w, r, user, _cardBanks, pageData{}, kit.Failure{Err: err, Form: string(bank) + "-link"})
	}
	if err == nil && len(res.NeedsLogin) == 0 {
		var synced domain.BankSync
		synced, err = h.app.Svc.Banks.Sync(ctx, user.ID, bank, user.Location(), true /* force */)
		res.NeedsLogin, res.Operations = synced.NeedsLogin, res.Operations+synced.Operations
	}
	return h.syncReply(w, r, user, pageData{Notice: kit.SyncNotice(ctx, res)}, err)
}

// syncReply answers a sync with the banks card and d, showing its notice, or err when
// the sync failed.
func (h *Handler) syncReply(w http.ResponseWriter, r *http.Request, user domain.User, d pageData, err error) error {
	if err != nil {
		// a failing bank is not a fault of this server; the card shows it too
		h.app.Log.WarnContext(r.Context(), "sync bank", slog.Any("error", err))
		d.Notice, d.Banks.SyncErr = "", err.Error()
	}
	if !kit.IsHTMX(r) && d.Notice != "" {
		h.app.SetNotice(w, d.Notice)
	}
	return h.settingsReply(w, r, user, _cardBanks, d, kit.Failure{})
}

// bankPicks returns the links of the accounts at bank not synced of the link form of
// r to the accounts chosen for them, 0 for a new one. An account may be chosen once.
func bankPicks(r *http.Request, bank domain.Bank, bankAccounts []domain.BankAccount) ([]domain.BankLink, error) {
	var picks []domain.BankLink
	chosen := make(map[int64]bool)
	for _, t := range bankAccounts {
		id := strconv.FormatInt(t.ID, 10)
		if t.Bank != bank || t.AccountID != 0 {
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

func (h *Handler) bankDisconnect(w http.ResponseWriter, r *http.Request, user domain.User) error {
	bank, err := h.pathBank(r)
	if err != nil {
		return err
	}
	if err := h.app.Svc.Banks.Disconnect(r.Context(), user.ID, bank); err != nil {
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
	res, err := h.app.Svc.Banks.SyncAll(ctx, user.ID, user.Location(), false /* force */)
	if err != nil {
		h.app.Log.WarnContext(ctx, "sync banks", slog.Any("error", err))
	}
	current, urlErr := url.Parse(r.Header.Get("HX-Current-URL"))
	switch {
	case len(res.NeedsLogin) > 0 && (urlErr != nil || current.Path != "/settings"):
		return h.app.Fragment(w, r, http.StatusOK, bankLoginAlert(kit.SyncNotice(ctx, res)))
	case res.Operations > 0:
		h.app.SetNotice(w, kit.SyncNotice(ctx, res))
		w.Header().Set("HX-Refresh", "true")
		return nil
	case err != nil:
		d := pageData{Banks: banksData{SyncErr: err.Error()}}
		return h.app.Fragment(w, r, http.StatusOK, kit.Toasts(kit.Page{User: &user}, true, toasts(d)))
	}
	// an empty answer removes the placeholder
	return nil
}
