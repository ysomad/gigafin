package kit

import (
	"net/url"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// DayGroup are the operations of a day, listed by OpRows.
type DayGroup struct {
	Date time.Time
	Ops  []domain.Operation
	// Continued is set on a day the previous page ended in, so its title is not repeated.
	Continued bool
}

// GroupByDay splits ops, ordered by date, into runs of one day.
func GroupByDay(ops []domain.Operation) []DayGroup {
	var days []DayGroup
	for _, op := range ops {
		if len(days) == 0 || !days[len(days)-1].Date.Equal(op.Date) {
			days = append(days, DayGroup{Date: op.Date})
		}
		days[len(days)-1].Ops = append(days[len(days)-1].Ops, op)
	}
	return days
}

// NoCategory is the category filter value of expenses and income without a category.
const NoCategory int64 = -1

// FilterFromQuery returns the operations filter of the query q of the operations list.
func (a *App) FilterFromQuery(q url.Values) (domain.Filter, error) {
	var in struct {
		Query    string `form:"q"`
		From     string `form:"from"`
		To       string `form:"to"`
		Account  int64  `form:"account"`  // 0 means all
		Category int64  `form:"category"` // 0 means all, NoCategory none
		Type     string `form:"type"`
		Min      string `form:"min"`
		Max      string `form:"max"`
		Tags     string `form:"tags"` // comma-separated
	}
	if err := a.Decoder.Decode(&in, q); err != nil {
		return domain.Filter{}, domain.NewError(errors.ErrInvalid, "invalid filter")
	}
	f := domain.Filter{Query: in.Query, AccountID: in.Account, CategoryID: in.Category}
	if in.Category == NoCategory {
		f.CategoryID, f.Uncategorized = 0, true
	}
	for tag := range strings.SplitSeq(in.Tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			f.Tags = append(f.Tags, tag)
		}
	}
	var err error
	if in.From != "" {
		if f.DateFrom, err = timex.ParseDate(in.From); err != nil {
			return f, domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM-DD", domain.Msg("From"), in.From)
		}
	}
	if in.To != "" {
		if f.DateTo, err = timex.ParseDate(in.To); err != nil {
			return f, domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM-DD", domain.Msg("To"), in.To)
		}
	}
	if in.Type != "" {
		if f.Type, err = domain.ParseOpType(in.Type); err != nil {
			return f, err
		}
	}
	if in.Min != "" {
		if f.AmountMin.Amount, err = money.Parse(in.Min); err != nil {
			return f, domain.NewError(errors.ErrInvalid, "%s: enter a number", domain.Msg("Amount from"))
		}
		f.AmountMin.Valid = true
	}
	if in.Max != "" {
		if f.AmountMax.Amount, err = money.Parse(in.Max); err != nil {
			return f, domain.NewError(errors.ErrInvalid, "%s: enter a number", domain.Msg("Amount to"))
		}
		f.AmountMax.Valid = true
	}
	return f, nil
}
