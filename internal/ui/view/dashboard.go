package view

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/a-h/templ"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/timex"
)

type Dashboard struct {
	Dash domain.Dashboard
	// Prev, Next, ComparePrev and CompareNext are adjacent months within Months,
	// empty when there is none.
	Prev        string
	Next        string
	ComparePrev string
	CompareNext string
	From        string
	To          string
	// Charts are income vs expense with net worth per currency, ordered by currency.
	Charts []TrendChart
	// Months are the months with operations, latest first, to pick Month and CompareMonth from.
	Months []timex.Month
}

// TrendChart is a trend and net worth of a currency as chart series, read by dashboard.js.
type TrendChart struct {
	Currency string    `json:"-"`
	Months   []int64   `json:"months"` // unix seconds of the first day
	Income   []float64 `json:"income"`
	Expense  []float64 `json:"expense"`
	// Balance is the month-end net worth, empty for a currency without one.
	Balance []float64 `json:"balance,omitempty"`
}

// ChartScript is the script element of the series of c as JSON, with their labels,
// read by dashboard.js. templ writes script content as is, so the element is written
// here; json.Marshal escapes <, > and & in strings, so the JSON can't end it.
func ChartScript(c TrendChart, income, expense, balance string) templ.ComponentFunc {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		b, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("marshal chart: %w", err)
		}
		_, err = fmt.Fprintf(w,
			`<script type="application/json" data-label-income="%s" data-label-expense="%s" data-label-balance="%s">%s</script>`,
			templ.EscapeString(income), templ.EscapeString(expense), templ.EscapeString(balance), b)
		if err != nil {
			return fmt.Errorf("write chart: %w", err)
		}
		return nil
	})
}

// CurrencyGroups splits items, ordered by currency, into runs of one currency.
func CurrencyGroups[T any](items []T, currency func(T) string) [][]T {
	var groups [][]T
	for i, it := range items {
		if i == 0 || currency(it) != currency(items[i-1]) {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], it)
	}
	return groups
}
