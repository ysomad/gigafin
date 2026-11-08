package view

import (
	"strconv"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/timex"
)

type Plans struct {
	Month timex.Month
	// Months are the months with operations, latest first, to pick Month from.
	Months []timex.Month
	// Prev and Next are the adjacent months within Months, empty at its ends.
	Prev string
	Next string
	From string
	To   string
	// Sections are the plans of expenses, then of income.
	Sections []PlanSection
	// Day of Days is how far the month has gone: Days for a past month, 0 for a future one
	Day  int
	Days int
	// Pace is Day as a share of Days in percent, the spending expected by now
	Pace int
	// Posted are the values of a plan form post that failed, to fill its inputs again.
	Posted PlanForm
	// Open is the "category:currency" of the plan row posted last, kept open.
	Open string
}

// PlanForm is a posted plan form: the new plan form, with New set, or the form of a
// plan row, whose amount message is keyed by planKey.
type PlanForm struct {
	New       bool   `form:"new"`
	Category  int64  `form:"category"`
	Month     string `form:"month"`
	Currency  string `form:"currency"`
	Amount    string `form:"amount"`
	Repeating bool   `form:"repeating"`
}

// PlanSection is the plans of a Kind of categories, with their Totals per currency.
type PlanSection struct {
	Kind    domain.Kind
	Totals  []domain.PlanRow
	Planned []PlanItem
}

// PlanItem is a plan row as shown, under its cat1 Parent for a cat2.
type PlanItem struct {
	Row    domain.PlanRow
	Parent string
}

// PlanNew is the new plan page: the form values of Posted, with Month parsed from
// it.
type PlanNew struct {
	Month      timex.Month
	Months     []timex.Month
	Categories []domain.Category
	Currencies []string
	Posted     PlanForm
}

// PlanKey returns the key of the amount message of the plan row form of category in
// currency.
func PlanKey(category int64, currency string) string {
	return "amount[" + strconv.FormatInt(category, 10) + ":" + currency + "]"
}
