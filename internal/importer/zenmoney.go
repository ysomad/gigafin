package importer

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// Column names of the export header.
const (
	_zenMoneyColDate            = "date"
	_zenMoneyColCategory        = "categoryName"
	_zenMoneyColPayee           = "payee"
	_zenMoneyColComment         = "comment"
	_zenMoneyColOutcomeAccount  = "outcomeAccountName"
	_zenMoneyColOutcome         = "outcome"
	_zenMoneyColOutcomeCurrency = "outcomeCurrencyShortTitle"
	_zenMoneyColIncomeAccount   = "incomeAccountName"
	_zenMoneyColIncome          = "income"
	_zenMoneyColIncomeCurrency  = "incomeCurrencyShortTitle"
	_zenMoneyColCreated         = "createdDate"
)

// _zenMoneyCols are the columns parseZenMoney reads, in the order of the export.
var _zenMoneyCols = []string{
	_zenMoneyColDate, _zenMoneyColCategory, _zenMoneyColPayee, _zenMoneyColComment,
	_zenMoneyColOutcomeAccount, _zenMoneyColOutcome, _zenMoneyColOutcomeCurrency,
	_zenMoneyColIncomeAccount, _zenMoneyColIncome, _zenMoneyColIncomeCurrency, _zenMoneyColCreated,
}

const (
	_zenMoneyComma         = ';'
	_zenMoneyCreatedLayout = time.DateTime
)

// _zenMoneyPayeeMCC finds the Merchant Category Code card payments carry in the payee, as in
// "SBER*5411*SAMOKAT"; the export has no MCC column.
var _zenMoneyPayeeMCC = regexp.MustCompile(`^[^*]+\*(\d{4})\*`)

// _zenMoneyNotExport starts errors of files of another format. All errors of
// parseZenMoney except reading failures are of kind errors.ErrInvalid.
const _zenMoneyNotExport domain.Msg = "not a Zenmoney CSV export"

// parseZenMoney reads a Zenmoney CSV transaction export. A row with an outcome is an
// expense, with an income an income, and with both on different accounts a transfer.
// Rows gigafin cannot store, such as transfers between currencies, are skipped. Dates
// are in loc; the time is taken from the creation time, read in loc, when it is on the
// date of the operation.
func parseZenMoney(r io.Reader, loc *time.Location) (domain.ParseResult, error) {
	return parseRecords(r, csvFormat{
		comma: _zenMoneyComma, lazyQuotes: true, notFormat: _zenMoneyNotExport,
		required: []string{
			_zenMoneyColDate, _zenMoneyColCategory, _zenMoneyColPayee, _zenMoneyColComment,
			_zenMoneyColOutcomeAccount, _zenMoneyColOutcome, _zenMoneyColOutcomeCurrency,
			_zenMoneyColIncomeAccount, _zenMoneyColIncome, _zenMoneyColIncomeCurrency,
		},
	}, loc, func(get func(string) string) (domain.Record, error) {
		return parseZenMoneyRow(get, loc)
	})
}

// parseZenMoneyRow parses a record. A row that will not be imported has SkipReason set; any
// other row must have every field valid.
func parseZenMoneyRow(get func(column string) string, loc *time.Location) (domain.Record, error) {
	row := domain.Record{
		Category: get(_zenMoneyColCategory),
		Payee:    get(_zenMoneyColPayee),
		Note:     get(_zenMoneyColComment),
	}
	if m := _zenMoneyPayeeMCC.FindStringSubmatch(row.Payee); m != nil {
		row.MCC = m[1]
	}
	// several tags are joined by ", ", the first one is the category; tags nest like
	// gigafin categories, but a tag name may contain " / ", which gigafin keeps for
	// joining cat1 and cat2
	row.Category, _, _ = strings.Cut(row.Category, ", ")
	if parent, child, ok := strings.Cut(row.Category, " / "); ok {
		row.Category = domain.FullName(
			strings.TrimSpace(parent),
			strings.ReplaceAll(strings.TrimSpace(child), " / ", "/"),
		)
	}

	date, err := time.ParseInLocation(time.DateOnly, get(_zenMoneyColDate), loc)
	if err != nil {
		return domain.Record{}, domain.NewError(
			errors.ErrInvalid,
			"%s: %q must be YYYY-MM-DD",
			_zenMoneyColDate,
			get(_zenMoneyColDate),
		)
	}
	row.Time, row.DateOnly = date, true
	if created, err := time.ParseInLocation(_zenMoneyCreatedLayout, get(_zenMoneyColCreated), loc); err == nil &&
		created.Format(time.DateOnly) == get(_zenMoneyColDate) {
		row.Time, row.DateOnly = created, false
	}
	return parseZenMoneyAmounts(row, get)
}

// parseZenMoneyAmounts sets the accounts, amount and currency of row from the outcome and
// income sides, or the reason to skip it.
func parseZenMoneyAmounts(row domain.Record, get func(name string) string) (domain.Record, error) {
	outcome, err := money.Parse(get(_zenMoneyColOutcome))
	if err != nil {
		return domain.Record{}, fmt.Errorf("%s: %w", _zenMoneyColOutcome, err)
	}
	income, err := money.Parse(get(_zenMoneyColIncome))
	if err != nil {
		return domain.Record{}, fmt.Errorf("%s: %w", _zenMoneyColIncome, err)
	}
	if outcome.IsNegative() || income.IsNegative() {
		return domain.Record{}, domain.NewError(
			errors.ErrInvalid,
			"%s, %s: must not be negative",
			_zenMoneyColOutcome,
			_zenMoneyColIncome,
		)
	}
	outAccount, inAccount := get(_zenMoneyColOutcomeAccount), get(_zenMoneyColIncomeAccount)
	outCurrency := strings.ToUpper(get(_zenMoneyColOutcomeCurrency))
	inCurrency := strings.ToUpper(get(_zenMoneyColIncomeCurrency))
	switch {
	case outcome.IsZero() && income.IsZero():
		row.Account, row.Currency = outAccount, outCurrency
		row.SkipReason = domain.SkipZeroAmount
		return row, nil
	case income.IsZero():
		row.Account, row.Amount, row.Currency = outAccount, outcome.Neg(), outCurrency
	case outcome.IsZero():
		row.Account, row.Amount, row.Currency = inAccount, income, inCurrency
	default:
		row.Account, row.ToAccount, row.Amount, row.Currency = outAccount, inAccount, outcome.Neg(), outCurrency
		row.Category = ""
		switch {
		case outAccount == inAccount:
			row.SkipReason = domain.SkipSameAccount
			return row, nil
		case outCurrency != inCurrency:
			row.SkipReason = domain.SkipTransferCurrencies
			return row, nil
		case !outcome.Equal(income):
			row.SkipReason = domain.SkipTransferAmounts
			return row, nil
		}
	}
	if !money.ValidCurrency(row.Currency) {
		return domain.Record{}, domain.NewError(errors.ErrInvalid, "currency: unsupported currency %q", row.Currency)
	}
	if outAccount == "" && !outcome.IsZero() {
		return domain.Record{}, domain.NewError(errors.ErrInvalid, "%s: empty", _zenMoneyColOutcomeAccount)
	}
	if inAccount == "" && !income.IsZero() {
		return domain.Record{}, domain.NewError(errors.ErrInvalid, "%s: empty", _zenMoneyColIncomeAccount)
	}
	return row, nil
}
