package importer

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// Column names of the export header.
const (
	_tbankColAccount     = "Имя счёта"
	_tbankColTime        = "Дата операции"
	_tbankColAmount      = "Сумма в валюте счёта"
	_tbankColCurrency    = "Валюта счёта"
	_tbankColStatus      = "Статус"
	_tbankColCategory    = "Категория по-умолчанию"
	_tbankColOwnCategory = "Ваша категория"
	_tbankColMCC         = "MCC"
	_tbankColDescription = "Описание"
	_tbankColMessage     = "Сообщение"
)

// _tbankCols are the columns parseTBank reads, in the order of the export.
var _tbankCols = []string{
	_tbankColAccount, _tbankColTime, _tbankColAmount, _tbankColCurrency, _tbankColStatus,
	_tbankColCategory, _tbankColOwnCategory, _tbankColMCC, _tbankColDescription, _tbankColMessage,
}

const (
	_tbankComma      = ';'
	_tbankStatusOK   = "Ок"
	_tbankTimeLayout = "02.01.2006 15:04:05"
)

// _tbankNotExport starts errors of files of another format. All errors of
// parseTBank except reading failures are of kind errors.ErrInvalid.
const _tbankNotExport domain.Msg = "not a T-Bank CSV export"

// parseTBank reads a T-Bank (Tinkoff) CSV operation export. Header lines may repeat,
// so several exports concatenated into one file parse as one. Skipped rows have a
// status other than "Ок" or a zero amount. Times are returned in loc.
func parseTBank(r io.Reader, loc *time.Location) (domain.ParseResult, error) {
	// export timestamps are Moscow time
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		msk = time.FixedZone("MSK", int((3 * time.Hour).Seconds()))
	}
	return parseRecords(r, csvFormat{
		comma: _tbankComma, lazyQuotes: true, notFormat: _tbankNotExport, repeatedHeader: true,
		required: []string{
			_tbankColAccount, _tbankColTime, _tbankColAmount, _tbankColCurrency, _tbankColStatus,
			_tbankColCategory, _tbankColDescription,
		},
	}, loc, func(get func(string) string) (domain.Record, error) {
		return parseTBankRow(get, msk)
	})
}

// parseTBankRow parses a record. A row that will not be imported has SkipReason set and
// never fails; a successful row must have every field valid.
func parseTBankRow(get func(column string) string, loc *time.Location) (domain.Record, error) {
	category := get(_tbankColOwnCategory)
	if category == "" {
		category = get(_tbankColCategory)
	}
	row := domain.Record{
		Account:  get(_tbankColAccount),
		Currency: strings.ToUpper(get(_tbankColCurrency)),
		Category: category,
		Payee:    get(_tbankColDescription),
		Note:     get(_tbankColMessage),
	}
	// the bank leaves it empty for transfers and fees
	if mcc := get(_tbankColMCC); domain.ValidMCC(mcc) {
		row.MCC = mcc
	}
	amount, amountErr := money.Parse(get(_tbankColAmount))
	if amountErr == nil {
		row.Amount = amount
	}
	t, timeErr := time.ParseInLocation(_tbankTimeLayout, get(_tbankColTime), loc)
	if timeErr == nil {
		row.Time = t
	}

	if status := get(_tbankColStatus); status != _tbankStatusOK {
		row.SkipReason, row.SkipCause = domain.SkipNotCompleted, status
		return row, nil
	}
	if amountErr != nil {
		return domain.Record{}, fmt.Errorf("%s: %w", _tbankColAmount, amountErr)
	}
	if amount.IsZero() {
		row.SkipReason = domain.SkipZeroAmount
		return row, nil
	}
	if timeErr != nil {
		return domain.Record{}, domain.NewError(
			errors.ErrInvalid,
			"%s: %q must be DD.MM.YYYY HH:MM:SS",
			_tbankColTime,
			get(_tbankColTime),
		)
	}
	if !money.ValidCurrency(row.Currency) {
		return domain.Record{}, domain.NewError(
			errors.ErrInvalid,
			"%s: unsupported currency %q",
			_tbankColCurrency,
			row.Currency,
		)
	}
	if row.Account == "" {
		return domain.Record{}, domain.NewError(errors.ErrInvalid, "%s: empty", _tbankColAccount)
	}
	return row, nil
}
