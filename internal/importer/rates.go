package importer

import (
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/timex"
)

// Column names of the rates header.
const (
	_ratesColDate = "date"
	_ratesColFrom = "from"
	_ratesColTo   = "to"
	_ratesColRate = "rate"
)

// _ratesNotFile starts errors of files of another format.
const _ratesNotFile domain.Msg = "not a rates CSV file"

// RatesHeader is the header of a rates file.
const RatesHeader = _ratesColDate + "," + _ratesColFrom + "," + _ratesColTo + "," + _ratesColRate

// ParseRates reads exchange rates of source in the format of Actual Budget: a UTF-8 CSV
// file, optionally with a BOM, separated by "," and starting with the header
// "date,from,to,rate". Columns may come in any order and unknown ones are ignored:
//
//	date  required, YYYY-MM-DD
//	from  required, ISO 4217 code
//	to    required, ISO 4217 code
//	rate  required, price of one from in to, with a decimal point
//
// One side of each row must be the pivot currency of source. Errors are of kind
// errors.ErrInvalid except reading failures.
func ParseRates(source domain.RateSource, r io.Reader) ([]domain.Rate, error) {
	var rates []domain.Rate
	err := readCSV(r, csvFormat{
		comma: ',', notFormat: _ratesNotFile, required: []string{_ratesColDate, _ratesColFrom, _ratesColTo, _ratesColRate},
	}, func(line int, get func(string) string) error {
		date, err := timex.ParseDate(get(_ratesColDate))
		if err != nil {
			return fmt.Errorf("line %d: %s: %w", line, _ratesColDate, err)
		}
		v, err := decimal.NewFromString(get(_ratesColRate))
		if err != nil {
			return domain.NewError(
				errors.ErrInvalid,
				"line %d: %s: %q is not a number",
				line,
				_ratesColRate,
				get(_ratesColRate),
			)
		}
		rate, err := domain.NewRate(source, strings.ToUpper(get(_ratesColFrom)),
			strings.ToUpper(get(_ratesColTo)), date, v)
		if err != nil {
			return domain.NewError(err, "line %d", line)
		}
		rates = append(rates, rate)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rates) == 0 {
		return nil, domain.NewError(errors.ErrInvalid, "no rates in file, the header must be %q", RatesHeader)
	}
	return rates, nil
}
