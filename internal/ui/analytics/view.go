package analytics

import (
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/timex"
)

type analyticsData struct {
	An domain.Analytics
	// Prev, Next, ComparePrev and CompareNext are adjacent months within Months,
	// empty when there is none.
	Prev        string
	Next        string
	ComparePrev string
	CompareNext string
	From        string
	To          string
	// Charts are income vs expense with net worth per currency, ordered by currency.
	Charts []trendChart
	// Months are the months with operations, latest first, to pick Month and CompareMonth from.
	Months []timex.Month
}

// trendChart is a trend and net worth of a currency as chart series, read by analytics.js.
type trendChart struct {
	Currency string    `json:"-"`
	Months   []int64   `json:"months"` // unix seconds of the first day
	Income   []float64 `json:"income"`
	Expense  []float64 `json:"expense"`
	// Balance is the month-end net worth, empty for a currency without one.
	Balance []float64 `json:"balance,omitempty"`
}

// currencyGroups splits items, ordered by currency, into runs of one currency.
func currencyGroups[T any](items []T, currency func(T) string) [][]T {
	var groups [][]T
	for i, it := range items {
		if i == 0 || currency(it) != currency(items[i-1]) {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], it)
	}
	return groups
}
