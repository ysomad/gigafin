package app

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/tbank"
)

// tbankProbe tries the T-Bank API without a database: it signs in, or refreshes the
// session saved in the output directory, and writes the accounts and operations the
// bank returns with the records gigafin makes of them, to compare with a CSV export.
// The files hold personal data and the session, so they are readable by the owner only.
// Requests are logged to log.
//
//nolint:cyclop,funlen // a linear script of steps, each reported as it goes
func tbankProbe(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("tbank probe", flag.ContinueOnError)
	phone := fs.String("phone", "", "phone to sign in with, +79991234567; needed without a saved session")
	out := fs.String("out", "tbank-probe", "directory for the session and the dumps")
	days := fs.Int("days", 30, "days of operations to fetch")
	csvPath := fs.String("csv", "", "T-Bank CSV export to compare the records with")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return fmt.Errorf("output dir: %w", err)
	}
	hc, err := newRussianHTTPClient(time.Minute)
	if err != nil {
		return err
	}
	c := tbank.New(hc, tbank.IDURL, tbank.APIURL, log)
	sessPath := filepath.Join(*out, "session.json")
	save := func(s tbank.Session) error {
		b, err := json.MarshalIndent(s, "", "  ") //nolint:gosec // G117: the probe saves the session on purpose, 0600
		if err != nil {
			return err
		}
		return os.WriteFile(sessPath, b, 0o600)
	}

	var sess tbank.Session
	if b, err := os.ReadFile(sessPath); err == nil { //nolint:gosec // G304: a path the operator gave
		if err := json.Unmarshal(b, &sess); err != nil {
			return fmt.Errorf("%s: %w", sessPath, err)
		}
		fmt.Printf("saved session of device %s, access token expires %s\n",
			sess.DeviceID, sess.ExpiresAt.Format(time.DateTime))
	}
	if sess.RefreshToken != "" {
		fresh, err := c.Refresh(ctx, sess)
		switch {
		case err == nil:
			fmt.Printf("refresh: ok, refresh token rotated: %t\n", fresh.RefreshToken != sess.RefreshToken)
			sess = fresh
			if err := save(sess); err != nil {
				return err
			}
		case errors.Is(err, tbank.ErrSessionExpired):
			fmt.Println("refresh: the bank rejected the saved session, signing in again")
			sess = tbank.Session{DeviceID: sess.DeviceID}
		default:
			return err
		}
	}
	if sess.RefreshToken == "" {
		if sess, err = probeLogin(ctx, c, sess.DeviceID, *phone, save); err != nil {
			return err
		}
	}

	now := time.Now()
	from := now.AddDate(0, 0, -*days)
	rawAccounts, err := c.Payload(ctx, sess, "/v1/accounts_light", nil)
	if err != nil {
		return err
	}
	rawOps, err := c.Payload(ctx, sess, "/v1/operations", map[string][]string{
		"start": {strconv.FormatInt(from.UnixMilli(), 10)},
		"end":   {strconv.FormatInt(now.UnixMilli(), 10)},
	})
	if err != nil {
		return err
	}
	var (
		accounts []tbank.Account
		ops      []tbank.Operation
	)
	if err := json.Unmarshal(rawAccounts, &accounts); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	if err := json.Unmarshal(rawOps, &ops); err != nil {
		return fmt.Errorf("operations: %w", err)
	}
	for name, raw := range map[string]json.RawMessage{"accounts.json": rawAccounts, "operations.json": rawOps} {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o600); err != nil {
			return err
		}
	}

	// the probe treats every tracked account as linked
	fmt.Printf("\naccounts: %d\n", len(accounts))
	var (
		tracked []tbank.Account
		linked  []string
	)
	for _, a := range accounts {
		if !a.Tracked() {
			fmt.Printf("  %-30s %-9s not tracked\n", a.Name, a.AccountType)
			continue
		}
		tracked, linked = append(tracked, a), append(linked, a.ID)
		fmt.Printf("  %-30s %-9s %s, balance %s\n", a.Name, a.AccountType, a.Currency.Name, a.Balance())
	}
	// operation times of exports are Moscow time
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return err
	}
	res := importer.FromTBank(tracked, linked, nil /* mapped */, ops, msk)
	fmt.Printf("operations: %d since %s, records %d, skipped %d\n",
		len(ops), from.Format(time.DateOnly), len(res.Rows), len(res.Skipped))
	reasons := map[string]int{}
	for _, r := range res.Skipped {
		reasons[r.SkipReason]++
	}
	for reason, n := range reasons {
		fmt.Printf("  skipped %-20s %d\n", reason, n)
	}

	f, err := os.OpenFile(filepath.Join(*out, "records.csv"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	records := [][]string{{"hash", "skip", "account", "time", "amount", "currency", "category", "payee", "mcc", "note"}}
	hashes := domain.HashRows(domain.ImportSourceTBank, res.Rows)
	record := func(hash string, r domain.Record) []string {
		return []string{
			hash, r.SkipReason, r.Account, r.Time.Format(time.DateTime), r.Amount.String(), r.Currency,
			r.Category, r.Payee, r.MCC, r.Note,
		}
	}
	for i, r := range res.Rows {
		records = append(records, record(hashes[i], r))
	}
	for _, r := range res.Skipped {
		records = append(records, record("", r))
	}
	if err := csv.NewWriter(f).WriteAll(records); err != nil {
		return fmt.Errorf("records.csv: %w", err)
	}
	fmt.Printf("\nwrote %s\n", strings.Join([]string{"accounts.json", "operations.json", "records.csv"}, ", "))
	if *csvPath == "" {
		return nil
	}

	// records of the export in the fetched period should have the same hashes
	cf, err := os.Open(*csvPath)
	if err != nil {
		return err
	}
	defer cf.Close()
	exp, err := importer.Parse(domain.ImportSourceTBank, cf, msk)
	if err != nil {
		return fmt.Errorf("%s: %w", *csvPath, err)
	}
	exported := map[string]bool{}
	var last time.Time
	for i, h := range domain.HashRows(domain.ImportSourceTBank, exp.Rows) {
		exported[h] = true
		if exp.Rows[i].Time.After(last) {
			last = exp.Rows[i].Time
		}
	}
	var matched, missing int
	for i, r := range res.Rows {
		if r.Time.After(last) {
			continue
		}
		if exported[hashes[i]] {
			matched++
			continue
		}
		missing++
		fmt.Printf("  not in export: %s %s %s %s %q\n",
			r.Time.Format(time.DateTime), r.Account, r.Amount, r.Currency, r.Payee)
	}
	fmt.Printf("export: %d records, API records up to its last one: %d matched, %d not in export\n",
		len(exp.Rows), matched, missing)
	return nil
}

// probeLogin signs in to T-Bank by phone, asking for each step on stdin, saves the
// session and checks that the refresh grant works on it, as every sync starts with it.
func probeLogin(
	ctx context.Context, c *tbank.Client, deviceID, phone string, save func(tbank.Session) error,
) (tbank.Session, error) {
	if phone == "" {
		return tbank.Session{}, errors.Error("-phone is required to sign in")
	}
	phone, err := domain.NormalizePhone(phone)
	if err != nil {
		return tbank.Session{}, err
	}
	l, err := c.StartLogin(ctx, deviceID, phone)
	if err != nil {
		return tbank.Session{}, err
	}
	in := bufio.NewReader(os.Stdin)
	var sess tbank.Session
	for done := false; !done; {
		value, err := readStep(in, l.Step)
		if err != nil {
			return tbank.Session{}, err
		}
		if sess, done, err = c.Confirm(ctx, l, value); err != nil {
			return tbank.Session{}, err
		}
	}
	fmt.Printf("signed in, device %s\n", sess.DeviceID)
	if err := save(sess); err != nil {
		return tbank.Session{}, err
	}
	fresh, err := c.Refresh(ctx, sess)
	if err != nil {
		return tbank.Session{}, fmt.Errorf("refresh right after sign-in: %w", err)
	}
	fmt.Printf("refresh: ok, refresh token rotated: %t\n", fresh.RefreshToken != sess.RefreshToken)
	return fresh, save(fresh)
}

// readStep asks for the input of a sign-in step: the password without echo, the rest
// as a line.
func readStep(in *bufio.Reader, step string) (string, error) {
	if step == tbank.StepPassword && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("T-Bank password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(b), nil
	}
	if step == tbank.StepOTP {
		fmt.Print("SMS code: ")
	} else {
		fmt.Printf("%s: ", step)
	}
	line, err := in.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", step, err)
	}
	return strings.TrimSpace(line), nil
}
