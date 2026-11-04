// Package importer parses files of every import source into gigafin records.
package importer

import (
	"io"
	"strings"
	"time"

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
