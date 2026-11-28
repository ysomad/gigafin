package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// Column names of the header.
const (
	_gigaFinColDate      = "date"
	_gigaFinColTime      = "time"
	_gigaFinColAccount   = "account"
	_gigaFinColAmount    = "amount"
	_gigaFinColCurrency  = "currency"
	_gigaFinColCategory  = "category"
	_gigaFinColPayee     = "payee"
	_gigaFinColNote      = "note"
	_gigaFinColToAccount = "to_account"
	_gigaFinColMCC       = "mcc"
	_gigaFinColOriginID  = "origin_id"
	_gigaFinColTags      = "tags"
)

// _gigaFinCols are the columns of the format in the order WriteGigaFin writes them.
var _gigaFinCols = []string{
	_gigaFinColDate, _gigaFinColTime, _gigaFinColAccount, _gigaFinColAmount, _gigaFinColCurrency,
	_gigaFinColCategory, _gigaFinColPayee, _gigaFinColNote, _gigaFinColToAccount, _gigaFinColMCC, _gigaFinColOriginID,
	_gigaFinColTags,
}

const _gigaFinComma = ';'

// _gigaFinNotExport starts errors of files of another format. All errors of
// parseGigaFin except reading failures are of kind errors.ErrInvalid.
const _gigaFinNotExport domain.Msg = "not a gigafin CSV file"

// parseGigaFin reads a file in the gigafin CSV format. Dates and times are in loc. Rows
// with a zero amount are skipped.
//
// The file is UTF-8, optionally with a BOM, separated by ";" and starts with a header.
// Columns may come in any order and unknown ones are ignored:
//
//	date        required, YYYY-MM-DD in the user time zone
//	time        optional, HH:MM or HH:MM:SS; empty imports the date only
//	account     required, account name
//	amount      required, signed decimal: negative is money out
//	currency    required, ISO 4217 code
//	category    optional, "Cat1 / Cat2"; empty is no category; ignored for transfers
//	payee       optional
//	note        optional
//	to_account  optional, makes a transfer of a negative amount from account to it
//	mcc         optional, Merchant Category Code: 4 digits
//	origin_id   optional, rows with the same origin_id are the parts of one split operation;
//	            not allowed on transfers
//	tags        optional, tag names separated by ","
func parseGigaFin(r io.Reader, loc *time.Location) (domain.ParseResult, error) {
	return parseRecords(r, csvFormat{
		comma: _gigaFinComma, lazyQuotes: true, notFormat: _gigaFinNotExport,
		required: []string{_gigaFinColDate, _gigaFinColAccount, _gigaFinColAmount, _gigaFinColCurrency},
	}, loc, func(get func(string) string) (domain.Record, error) {
		return parseGigaFinRow(get, loc)
	})
}

// parseGigaFinRow parses a record. A row with a zero amount has SkipReason set; any other
// row must have every field valid.
func parseGigaFinRow(get func(column string) string, loc *time.Location) (domain.Record, error) {
	row := domain.Record{
		Account:   get(_gigaFinColAccount),
		ToAccount: get(_gigaFinColToAccount),
		Currency:  strings.ToUpper(get(_gigaFinColCurrency)),
		Category:  get(_gigaFinColCategory),
		Payee:     get(_gigaFinColPayee),
		Note:      get(_gigaFinColNote),
		MCC:       get(_gigaFinColMCC),
		Group:     get(_gigaFinColOriginID),
		// names are trimmed and checked when the row is committed
		Tags: strings.FieldsFunc(get(_gigaFinColTags), func(r rune) bool { return r == ',' }),
	}
	if row.IsTransfer() {
		row.Category = ""
	}

	date, err := time.ParseInLocation(time.DateOnly, get(_gigaFinColDate), loc)
	if err != nil {
		return domain.Record{}, domain.NewError(
			errors.ErrInvalid,
			"%s: %q must be YYYY-MM-DD",
			_gigaFinColDate,
			get(_gigaFinColDate),
		)
	}
	row.Time, row.DateOnly = date, true
	if clock := get(_gigaFinColTime); clock != "" {
		layout := "15:04"
		if strings.Count(clock, ":") == 2 {
			layout = time.TimeOnly
		}
		t, err := time.ParseInLocation(time.DateOnly+" "+layout, get(_gigaFinColDate)+" "+clock, loc)
		if err != nil {
			return domain.Record{}, domain.NewError(
				errors.ErrInvalid,
				"%s: %q must be HH:MM or HH:MM:SS",
				_gigaFinColTime,
				clock,
			)
		}
		row.Time, row.DateOnly = t, false
	}

	if row.Amount, err = money.Parse(get(_gigaFinColAmount)); err != nil {
		return domain.Record{}, fmt.Errorf("%s: %w", _gigaFinColAmount, err)
	}
	if row.Amount.IsZero() {
		row.SkipReason = domain.SkipZeroAmount
		return row, nil
	}
	if row.MCC != "" && !domain.ValidMCC(row.MCC) {
		return domain.Record{}, domain.NewError(errors.ErrInvalid, "%s: %q must be 4 digits", _gigaFinColMCC, row.MCC)
	}
	if !money.ValidCurrency(row.Currency) {
		return domain.Record{}, domain.NewError(
			errors.ErrInvalid,
			"%s: unsupported currency %q",
			_gigaFinColCurrency,
			row.Currency,
		)
	}
	if row.Account == "" {
		return domain.Record{}, domain.NewError(errors.ErrInvalid, "%s: empty", _gigaFinColAccount)
	}
	if row.IsTransfer() {
		if row.Group != "" {
			return domain.Record{}, domain.NewError(
				errors.ErrInvalid,
				"%s: transfers are not split",
				_gigaFinColOriginID,
			)
		}
		if row.ToAccount == row.Account {
			return domain.Record{}, domain.NewError(
				errors.ErrInvalid,
				"%s: must differ from %s",
				_gigaFinColToAccount,
				_gigaFinColAccount,
			)
		}
		if !row.Amount.IsNegative() {
			return domain.Record{}, domain.NewError(
				errors.ErrInvalid,
				"%s: a transfer must be negative",
				_gigaFinColAmount,
			)
		}
	}
	return row, nil
}

// WriteGigaFin writes ops in the gigafin CSV format read by parseGigaFin, in the given
// order. accounts and categories map ids to names, categories as "Cat1 / Cat2".
//
// Expenses and income that are parts of one split get its origin as origin_id, so an import
// splits them again. Re-importing loses what the format cannot hold: adjustments come
// back as expense or income without a category.
func WriteGigaFin(w io.Writer, ops []domain.Operation, accounts, categories map[int64]string) error {
	split := func(op domain.Operation) bool {
		return op.IsSplit() && op.Type.Categorized()
	}
	// a part whose split has no other part in ops is written as a plain operation
	parts := make(map[int64]int)
	for _, op := range ops {
		if split(op) {
			parts[op.OriginID]++
		}
	}

	cw := csv.NewWriter(w)
	cw.Comma = _gigaFinComma
	if err := cw.Write(_gigaFinCols); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	for _, op := range ops {
		var id string
		if split(op) && parts[op.OriginID] > 1 {
			id = strconv.FormatInt(op.OriginID, 10)
		}
		err := cw.Write([]string{
			timex.FormatDate(op.Date), op.Time, accounts[op.AccountID], op.SourceDelta().Fixed(op.Currency),
			op.Currency, categories[op.CategoryID], op.Payee, op.Note, accounts[op.ToAccountID], op.MCC, id,
			strings.Join(op.Tags, ","),
		})
		if err != nil {
			return fmt.Errorf("write csv: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return nil
}
