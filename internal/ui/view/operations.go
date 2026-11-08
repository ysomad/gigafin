package view

import (
	"time"

	"github.com/ysomad/gigafin/internal/domain"
)

type DayGroup struct {
	Date time.Time
	Ops  []domain.Operation
	// Continued is set on a day the previous page ended in, so its title is not repeated.
	Continued bool
}

type Ops struct {
	Days     []DayGroup
	MoreURL  string
	Filter   domain.Filter
	Filtered bool
	// FilterCount is the number of Filter fields set in the filter dialog, all but Query.
	FilterCount int
	Accounts    []domain.Account
	AccByID     map[int64]domain.Account
	Categories  []domain.Category
	CatNames    map[int64]string
	Tags        []string
	// RunURL runs rules on the operations matching Filter.
	RunURL string
	// Changed is the number of operations rules just changed, -1 when they did not run.
	Changed int
}

type SplitForm struct {
	CategoryID int64
	Amount     string
	Note       string
}

// OpForm is decoded from the operation form; untagged fields are view only.
type OpForm struct {
	ID int64
	// ImportID and OriginID are posted back only to re-render the form; the service
	// ignores them.
	ImportID    int64         `form:"import_id"`
	OriginID    int64         `form:"origin_id"`
	Type        domain.OpType `form:"type"`
	AccountID   int64         `form:"account"`    // 0 is not found
	ToAccountID int64         `form:"to_account"` // 0 is not found
	CategoryID  int64         `form:"category"`   // 0 is reported by validation
	Amount      string        `form:"amount"`
	Date        string        `form:"date"`
	Time        string        `form:"time"`
	Payee       string        `form:"payee"`
	Note        string        `form:"note"`
	// SourcePayee is the payee as the bank sent it.
	SourcePayee string
	// Tags are comma separated names.
	Tags string `form:"tags"`
	// Parts are the rows of the split page of a saved expense or income.
	Parts      []SplitForm
	Accounts   []domain.Account
	Categories []domain.Category
	// AllTags are the user's tags to pick from.
	AllTags []string
}
