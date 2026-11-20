package operations

import (
	"net/url"
	"strconv"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// opList is the operations page: a page of operations matching Filter.
type opList struct {
	// BankSync is set when the user syncs a bank, to sync it from the list, last at
	// BankSyncAt, zero before the first sync.
	BankSync   bool
	BankSyncAt time.Time
	// Mismatched are the bank accounts whose balance differs from the account each
	// syncs with.
	Mismatched []domain.BankAccount
	Days       []kit.DayGroup
	MoreURL    string
	Filter     domain.Filter
	Filtered   bool
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

// opSearch is the search page: the operations matching Query, none while it is empty.
type opSearch struct {
	Query    string
	Days     []kit.DayGroup
	MoreURL  string
	AccByID  map[int64]domain.Account
	CatNames map[int64]string
}

// splitPart is a part of the split form.
type splitPart struct {
	CategoryID int64
	Amount     string
	Note       string
}

// opForm is decoded from the operation form; untagged fields are view only.
type opForm struct {
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
	// Back is the page the edit was opened from, which saving, deleting and cancelling
	// return to.
	Back       string `form:"back"`
	Accounts   []domain.Account
	Categories []domain.Category
	// AllTags are the user's tags to pick from.
	AllTags []string
}

// returnURL is Back at the operation, its list running through it.
func (f opForm) returnURL() string {
	u, err := url.Parse(f.Back)
	if err != nil || f.Back == "" {
		u = &url.URL{Path: "/operations"}
	}
	q := u.Query()
	q.Set("at", strconv.FormatInt(f.ID, 10))
	u.RawQuery = q.Encode()
	return u.String()
}

// opSplit is the split page of a saved expense or income: its parts, the operation as
// the first one until the page is posted.
type opSplit struct {
	ID         int64
	OriginID   int64
	Type       domain.OpType
	Amount     string
	Payee      string
	Parts      []splitPart
	Categories []domain.Category
}
