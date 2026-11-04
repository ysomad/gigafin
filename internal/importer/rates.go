package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/unicode"

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
	cr := csv.NewReader(unicode.UTF8BOM.NewDecoder().Reader(r))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true

	var (
		rates []domain.Rate
		idx   map[string]int
	)
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if _, ok := errors.AsType[*csv.ParseError](err); ok {
			return nil, fmt.Errorf("%w: %w", err, errors.ErrInvalid)
		}
		if err != nil {
			return nil, fmt.Errorf("read csv: %w", err)
		}
		line, _ := cr.FieldPos(0)
		if idx == nil {
			idx = make(map[string]int, len(rec))
			for i, name := range rec {
				idx[strings.ToLower(strings.TrimSpace(name))] = i
			}
			for _, name := range [...]string{_ratesColDate, _ratesColFrom, _ratesColTo, _ratesColRate} {
				if _, ok := idx[name]; !ok {
					return nil, domain.NewError(
						errors.ErrInvalid,
						"line %d: missing column %q, the header must be %q",
						line,
						name,
						RatesHeader,
					)
				}
			}
			continue
		}
		get := func(name string) string {
			if i := idx[name]; i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if get(_ratesColDate) == "" && get(_ratesColFrom) == "" && get(_ratesColTo) == "" &&
			get(_ratesColRate) == "" {
			continue
		}
		date, err := timex.ParseDate(get(_ratesColDate))
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", line, _ratesColDate, err)
		}
		v, err := decimal.NewFromString(get(_ratesColRate))
		if err != nil {
			return nil, domain.NewError(
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
			return nil, domain.NewError(err, "line %d", line)
		}
		rates = append(rates, rate)
	}
	if len(rates) == 0 {
		return nil, domain.NewError(errors.ErrInvalid, "no rates in file, the header must be %q", RatesHeader)
	}
	return rates, nil
}
