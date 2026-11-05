package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"

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
	// _tbankDefaultCategory is the category of rows the bank left uncategorized.
	_tbankDefaultCategory = "Другое"
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
	// drops the byte order mark in front of the file
	cr := csv.NewReader(unicode.UTF8BOM.NewDecoder().Reader(r))
	cr.Comma = _tbankComma
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = true

	var (
		res domain.ParseResult
		idx map[string]int
	)
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if _, ok := errors.AsType[*csv.ParseError](err); ok {
			return domain.ParseResult{}, fmt.Errorf("%w: %w", err, errors.ErrInvalid)
		}
		if err != nil {
			return domain.ParseResult{}, fmt.Errorf("read csv: %w", err)
		}
		line, _ := cr.FieldPos(0)
		if len(rec) > 0 && strings.TrimSpace(strings.TrimPrefix(rec[0], "\ufeff")) == _tbankColAccount {
			idx, err = tbankHeader(rec)
			if err != nil {
				return domain.ParseResult{}, domain.NewError(err, "line %d", line)
			}
			continue
		}
		if idx == nil {
			return domain.ParseResult{}, domain.NewError(
				errors.ErrInvalid,
				"line %d: %s: header %q not found",
				line,
				_tbankNotExport,
				_tbankColAccount,
			)
		}
		blank := true
		for _, f := range rec {
			if strings.TrimSpace(f) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		row, err := parseTBankRow(rec, idx, msk)
		if err != nil {
			return domain.ParseResult{}, domain.NewError(err, "line %d", line)
		}
		row.Line = line
		if !row.Time.IsZero() {
			row.Time = row.Time.In(loc)
		}
		if row.SkipReason != "" {
			res.Skipped = append(res.Skipped, row)
			continue
		}
		res.Rows = append(res.Rows, row)
	}
	if idx == nil {
		return domain.ParseResult{}, domain.NewError(errors.ErrInvalid, "%s: empty file", _tbankNotExport)
	}
	if len(res.Rows) == 0 && len(res.Skipped) == 0 {
		return domain.ParseResult{}, domain.NewError(errors.ErrInvalid, "%s: no operations in file", _tbankNotExport)
	}
	return res, nil
}

func tbankHeader(rec []string) (map[string]int, error) {
	idx := make(map[string]int, len(rec))
	for i, name := range rec {
		idx[strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))] = i
	}
	required := [...]string{
		_tbankColAccount, _tbankColTime, _tbankColAmount, _tbankColCurrency, _tbankColStatus,
		_tbankColCategory, _tbankColDescription,
	}
	for _, name := range required {
		if _, ok := idx[name]; !ok {
			return nil, domain.NewError(errors.ErrInvalid, "%s: missing column %q", _tbankNotExport, name)
		}
	}
	return idx, nil
}

// parseTBankRow parses a record. A row that will not be imported has SkipReason set and
// never fails; a successful row must have every field valid.
func parseTBankRow(rec []string, idx map[string]int, loc *time.Location) (domain.Record, error) {
	get := func(name string) string {
		i, ok := idx[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	category := get(_tbankColOwnCategory)
	if category == "" {
		category = get(_tbankColCategory)
	}
	if category == "" {
		category = _tbankDefaultCategory
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
		row.SkipReason = "status " + strconv.Quote(status)
		return row, nil
	}
	if amountErr != nil {
		return domain.Record{}, fmt.Errorf("%s: %w", _tbankColAmount, amountErr)
	}
	if amount.IsZero() {
		row.SkipReason = "zero amount"
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
