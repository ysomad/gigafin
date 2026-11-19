// Package importer parses files of every import source into gigafin records.
package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

// Parse reads a file of src. Dates and times without a zone are read in loc. Errors are
// of kind errors.ErrInvalid except reading failures.
func Parse(src domain.ImportSource, r io.Reader, loc *time.Location) (domain.ParseResult, error) {
	switch src {
	case domain.ImportSourceGigaFin:
		return parseGigaFin(r, loc)
	case domain.ImportSourceTBank:
		return parseTBank(r, loc)
	case domain.ImportSourceZenMoney:
		return parseZenMoney(r, loc)
	default:
		return domain.ParseResult{}, domain.NewError(errors.ErrInvalid, "source: unknown %q", src)
	}
}

// Header returns the header line of a file of src with the columns Parse reads, joined
// by the separator of the format. It is empty for an unknown source.
func Header(src domain.ImportSource) string {
	switch src {
	case domain.ImportSourceGigaFin:
		return strings.Join(_gigaFinCols, string(_gigaFinComma))
	case domain.ImportSourceTBank:
		return strings.Join(_tbankCols, string(_tbankComma))
	case domain.ImportSourceZenMoney:
		return strings.Join(_zenMoneyCols, string(_zenMoneyComma))
	default:
		return ""
	}
}

// csvFormat is a CSV file format read by readCSV.
type csvFormat struct {
	comma rune
	// lazyQuotes allows quotes in unquoted fields, as bank exports have
	lazyQuotes bool
	// notFormat starts errors of files of another format
	notFormat domain.Msg
	// required are the columns a header must have; the first starts a header
	required []string
	// repeatedHeader allows the header again further down, as in exports concatenated
	// into one file; without it the first line is the header
	repeatedHeader bool
}

// readCSV reads r in format f and calls row with the line and the fields of each
// record after a header that is not blank. get returns the trimmed field of a column,
// named in any case, empty when the record has none. Errors are of kind
// errors.ErrInvalid except reading failures.
func readCSV(r io.Reader, f csvFormat, row func(line int, get func(column string) string) error) error {
	// drops the byte order mark Excel and banks put in front of UTF-8 files
	cr := csv.NewReader(unicode.UTF8BOM.NewDecoder().Reader(r))
	cr.Comma = f.comma
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = f.lazyQuotes
	cr.ReuseRecord = true

	// key trims the column names of a header, a mark of a file appended to another
	// included
	key := func(name string) string {
		return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))
	}
	var idx map[string]int
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if _, ok := errors.AsType[*csv.ParseError](err); ok {
			return fmt.Errorf("%w: %w", err, errors.ErrInvalid)
		}
		if err != nil {
			return fmt.Errorf("read csv: %w", err)
		}
		line, _ := cr.FieldPos(0)
		header := idx == nil
		if f.repeatedHeader {
			header = len(rec) > 0 && key(rec[0]) == key(f.required[0])
			if !header && idx == nil {
				return domain.NewError(
					errors.ErrInvalid, "line %d: %s: header %q not found", line, f.notFormat, f.required[0],
				)
			}
		}
		if header {
			idx = make(map[string]int, len(rec))
			for i, name := range rec {
				idx[key(name)] = i
			}
			for _, name := range f.required {
				if _, ok := idx[key(name)]; !ok {
					return domain.NewError(errors.ErrInvalid, "line %d: %s: missing column %q", line, f.notFormat, name)
				}
			}
			continue
		}
		get := func(column string) string {
			i, ok := idx[key(column)]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		blank := true
		for _, field := range rec {
			if strings.TrimSpace(field) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		if err := row(line, get); err != nil {
			return err
		}
	}
	if idx == nil {
		return domain.NewError(errors.ErrInvalid, "%s: empty file", f.notFormat)
	}
	return nil
}

// parseRecords reads the records of a file in format f, parsed by parse, with times in
// loc, and splits off the skipped ones.
func parseRecords(
	r io.Reader, f csvFormat, loc *time.Location, parse func(get func(column string) string) (domain.Record, error),
) (domain.ParseResult, error) {
	var res domain.ParseResult
	err := readCSV(r, f, func(line int, get func(string) string) error {
		row, err := parse(get)
		if err != nil {
			return domain.NewError(err, "line %d", line)
		}
		row.Line = line
		if !row.Time.IsZero() {
			row.Time = row.Time.In(loc)
		}
		if row.SkipReason != "" {
			res.Skipped = append(res.Skipped, row)
		} else {
			res.Rows = append(res.Rows, row)
		}
		return nil
	})
	if err != nil {
		return domain.ParseResult{}, err
	}
	if len(res.Rows) == 0 && len(res.Skipped) == 0 {
		return domain.ParseResult{}, domain.NewError(errors.ErrInvalid, "%s: no operations in file", f.notFormat)
	}
	return res, nil
}
