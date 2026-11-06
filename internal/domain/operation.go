package domain

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// OpType is the type of an operation.
type OpType string

const (
	OpExpense    OpType = "expense"
	OpIncome     OpType = "income"
	OpTransfer   OpType = "transfer"
	OpAdjustment OpType = "adjustment"
)

// OpTypes returns all operation types.
func OpTypes() []OpType {
	return []OpType{OpExpense, OpIncome, OpTransfer, OpAdjustment}
}

// ParseOpType validates an operation type string.
func ParseOpType(s string) (OpType, error) {
	t := OpType(s)
	if !slices.Contains(OpTypes(), t) {
		return "", NewError(errors.ErrInvalid, "type: must be expense, income, transfer or adjustment")
	}
	return t, nil
}

// Operation is an expense, income, transfer or balance adjustment.
type Operation struct {
	ID          int64
	Type        OpType
	AccountID   int64
	ToAccountID int64
	// CategoryID is the category of an expense or income, 0 for transfers and adjustments.
	CategoryID int64
	// OriginID is the id of the operation a split started from, shared by all parts of
	// the split including that operation; 0 when the operation was never split.
	OriginID int64
	Currency string
	// Amount is positive, except for adjustments where the sign is the balance change.
	Amount money.Amount
	// Date is a calendar day, see timex.
	Date time.Time
	// Time is the optional wall clock time "HH:MM".
	Time  string
	Payee string
	// SourcePayee is the payee as the bank sent it, set on import only and kept on
	// update, so rules match it after the payee is renamed; empty when entered by hand.
	SourcePayee string
	Note        string
	// MCC is the Merchant Category Code of a card payment, set on import only and
	// kept on update; empty when the source has none.
	MCC string
	// Tags are tag names, sorted.
	Tags      []string
	ImportID  int64
	CreatedAt time.Time
	// UpdatedAt is zero until the operation is first changed.
	UpdatedAt time.Time
}

// IsSplit reports whether the operation is a part of a split.
func (o Operation) IsSplit() bool {
	return o.OriginID != 0
}

// Kind returns the category kind of an expense or income.
func (o Operation) Kind() Kind {
	if o.Type == OpIncome {
		return KindIncome
	}
	return KindExpense
}

// Input returns the input that replaces the operation with itself, for changing it.
func (o Operation) Input() OperationInput {
	return OperationInput{
		Type:        o.Type,
		AccountID:   o.AccountID,
		ToAccountID: o.ToAccountID,
		CategoryID:  o.CategoryID,
		Amount:      o.Amount,
		Date:        o.Date,
		Time:        o.Time,
		Payee:       o.Payee,
		SourcePayee: o.SourcePayee,
		Note:        o.Note,
		MCC:         o.MCC,
		Tags:        slices.Clone(o.Tags),
	}
}

// OperationInput creates or replaces an operation.
type OperationInput struct {
	Type        OpType
	AccountID   int64
	ToAccountID int64
	// CategoryID is required for expense and income and must be 0 otherwise.
	CategoryID int64
	Amount     money.Amount
	Date       time.Time
	Time       string
	Payee      string
	// SourcePayee is stored on create only, see Operation.SourcePayee.
	SourcePayee string
	Note        string
	// MCC is stored on create only, see Operation.MCC.
	MCC string
	// Tags are tag names; a tag is created on first use.
	Tags []string
}

// ImportRef links an operation to the import row that created it.
type ImportRef struct {
	ImportID int64
	Hash     string
}

// NewOperation validates in against its account acc, the destination account to of
// a transfer and the category cat of an expense or income, each nil when not loaded. A
// tag that matches one of the user's tags known ignoring case takes its spelling.
func NewOperation(in OperationInput, acc Account, to *Account, cat *Category, known []string) (Operation, error) {
	typ, err := ParseOpType(string(in.Type))
	if err != nil {
		return Operation{}, err
	}
	date, tm, err := parseDateTime(in.Date, in.Time)
	if err != nil {
		return Operation{}, err
	}
	op := Operation{
		Type:      typ,
		AccountID: acc.ID,
		Currency:  acc.Currency,
		Amount:    in.Amount,
		Date:      date,
		Time:      tm,
	}
	op.Payee = strings.TrimSpace(in.Payee)
	if utf8.RuneCountInString(op.Payee) > MaxTextLen {
		return Operation{}, NewError(errors.ErrInvalid, "payee: too long")
	}
	op.SourcePayee = strings.TrimSpace(in.SourcePayee)
	if utf8.RuneCountInString(op.SourcePayee) > MaxTextLen {
		return Operation{}, NewError(errors.ErrInvalid, "bank payee: too long")
	}
	op.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(op.Note) > MaxTextLen {
		return Operation{}, NewError(errors.ErrInvalid, "note: too long")
	}
	if op.MCC = strings.TrimSpace(in.MCC); op.MCC != "" && !ValidMCC(op.MCC) {
		return Operation{}, NewError(errors.ErrInvalid, "mcc: must be 4 digits")
	}
	if op.Tags, err = newTags(in.Tags, known); err != nil {
		return Operation{}, err
	}
	if _, err := in.Amount.ToMinor(acc.Currency); err != nil {
		return Operation{}, NewError(errors.ErrInvalid, "amount: not a valid amount in %s", acc.Currency)
	}

	switch typ {
	case OpTransfer:
		err = op.setTransfer(in, to)
	case OpAdjustment:
		if in.CategoryID != 0 {
			return Operation{}, NewError(errors.ErrInvalid, "category: adjustments have no category")
		}
		if in.Amount.IsZero() {
			return Operation{}, NewError(errors.ErrInvalid, "amount: must not be zero")
		}
	case OpExpense, OpIncome:
		err = op.setCategory(in, cat)
	}
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

// newTags trims names, drops empty ones and duplicates ignoring case, and sorts them.
// A name matching a known tag ignoring case takes its spelling.
func newTags(names, known []string) ([]string, error) {
	var tags []string
	for _, n := range names {
		n = strings.Join(strings.Fields(n), " ")
		if n == "" {
			continue
		}
		if utf8.RuneCountInString(n) > MaxTagLen {
			return nil, NewError(errors.ErrInvalid, "tags: %q: longer than %d characters", n, MaxTagLen)
		}
		if strings.Contains(n, ",") {
			return nil, NewError(errors.ErrInvalid, "tags: %q: must not contain a comma", n)
		}
		if i := slices.IndexFunc(known, func(k string) bool { return strings.EqualFold(k, n) }); i >= 0 {
			n = known[i]
		}
		if !slices.ContainsFunc(tags, func(t string) bool { return strings.EqualFold(t, n) }) {
			tags = append(tags, n)
		}
	}
	if len(tags) > MaxTags {
		return nil, NewError(errors.ErrInvalid, "tags: at most %d", MaxTags)
	}
	slices.Sort(tags)
	return tags, nil
}

// ValidMCC reports whether s is a Merchant Category Code: 4 digits.
func ValidMCC(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (o *Operation) setTransfer(in OperationInput, to *Account) error {
	if in.CategoryID != 0 {
		return NewError(errors.ErrInvalid, "category: transfers have no category")
	}
	if !in.Amount.IsPositive() {
		return NewError(errors.ErrInvalid, "amount: must be positive")
	}
	if in.ToAccountID == o.AccountID {
		return NewError(errors.ErrInvalid, "destination account: must differ from the source account")
	}
	if to == nil {
		return NewError(errors.ErrInvalid, "destination account: destination account not found")
	}
	if to.Currency != o.Currency {
		return NewError(
			errors.ErrInvalid, "cannot transfer %s to a %s account", o.Currency, to.Currency,
		)
	}
	o.ToAccountID = to.ID
	return nil
}

func (o *Operation) setCategory(in OperationInput, cat *Category) error {
	if in.CategoryID == 0 {
		return NewError(errors.ErrInvalid, "category: required")
	}
	if cat == nil || cat.ID != in.CategoryID {
		return NewError(errors.ErrInvalid, "category: category not found")
	}
	if cat.Kind != o.Kind() {
		return NewError(errors.ErrInvalid, "category: category %q is not an %s category", cat.Name, Msg(o.Kind()))
	}
	if !in.Amount.IsPositive() {
		return NewError(errors.ErrInvalid, "amount: must be positive")
	}
	o.CategoryID = cat.ID
	return nil
}

// SplitPart is one categorized part an expense or income is split into.
type SplitPart struct {
	CategoryID int64
	Amount     money.Amount
	// Note is the note of the part, empty keeps the note of the operation.
	Note string
}

// SplitOperation splits the expense or income op into parts that add up to its amount,
// validated against the categories by id. The first operation returned is op itself
// with the first part, the others are new. All of them share the OriginID of op, or
// its ID when op is not a part of a split yet.
func SplitOperation(op Operation, parts []SplitPart, cats map[int64]Category) ([]Operation, error) {
	const maxParts = 50
	if op.Type != OpExpense && op.Type != OpIncome {
		return nil, NewError(errors.ErrInvalid, "type: only expenses and income can be split")
	}
	if len(parts) < 2 {
		return nil, NewError(errors.ErrInvalid, "parts: at least 2 parts")
	}
	if len(parts) > maxParts {
		return nil, NewError(errors.ErrInvalid, "parts: at most %d parts", maxParts)
	}
	origin := op.OriginID
	if origin == 0 {
		origin = op.ID
	}
	sum := money.Amount{}
	out := make([]Operation, len(parts))
	for i, p := range parts {
		c, ok := cats[p.CategoryID]
		if !ok {
			return nil, NewError(errors.ErrInvalid, "part %d: category not found", i+1)
		}
		if c.Kind != op.Kind() {
			return nil, NewError(
				errors.ErrInvalid,
				"part %d: category %q is not an %s category",
				i+1,
				c.Name,
				Msg(op.Kind()),
			)
		}
		if _, err := p.Amount.ToMinor(op.Currency); err != nil {
			return nil, NewError(errors.ErrInvalid, "part %d: not a valid amount in %s", i+1, op.Currency)
		}
		if !p.Amount.IsPositive() {
			return nil, NewError(errors.ErrInvalid, "part %d: amount must be positive", i+1)
		}
		part := op
		if i > 0 {
			part.ID = 0
		}
		part.CategoryID, part.Amount, part.OriginID = c.ID, p.Amount, origin
		if note := strings.TrimSpace(p.Note); note != "" {
			if utf8.RuneCountInString(note) > MaxTextLen {
				return nil, NewError(errors.ErrInvalid, "part %d: note too long", i+1)
			}
			part.Note = note
		}
		sum = sum.Add(p.Amount)
		out[i] = part
	}
	if !sum.Equal(op.Amount) {
		return nil, NewError(
			errors.ErrInvalid,
			"parts add up to %s, not %s",
			sum.Fixed(op.Currency),
			op.Amount.Fixed(op.Currency),
		)
	}
	return out, nil
}

func parseDateTime(date time.Time, tm string) (time.Time, string, error) {
	if date.IsZero() {
		return time.Time{}, "", NewError(errors.ErrInvalid, "date: required")
	}
	if strings.TrimSpace(tm) == "" {
		return timex.Date(date), "", nil
	}
	clock, err := timex.ParseClock(tm)
	if err != nil {
		return time.Time{}, "", NewError(errors.ErrInvalid, "time: must be HH:MM")
	}
	return timex.Date(date), clock, nil
}

// Filter selects operations. Zero fields match everything.
type Filter struct {
	AccountID  int64
	CategoryID int64
	Type       OpType
	// DateFrom and DateTo are inclusive calendar days, zero when unset.
	DateFrom time.Time
	DateTo   time.Time
	Query    string
	// AmountMin and AmountMax are inclusive bounds of the absolute amount in any
	// currency, unset when not Valid.
	AmountMin money.NullAmount
	AmountMax money.NullAmount
	// Tags are tag names, matched exactly; an operation matches any of them.
	Tags []string
}

// Filtered reports whether any field is set.
func (f Filter) Filtered() bool {
	return f.AccountID != 0 || f.CategoryID != 0 || f.Type != "" || !f.DateFrom.IsZero() ||
		!f.DateTo.IsZero() || f.Query != "" || f.AmountMin.Valid || f.AmountMax.Valid || len(f.Tags) > 0
}

// Validate checks that amount bounds are not negative and min is not above max.
func (f Filter) Validate() error {
	if f.AmountMin.Valid && f.AmountMin.Amount.IsNegative() {
		return NewError(errors.ErrInvalid, "amount min: must not be negative")
	}
	if f.AmountMax.Valid && f.AmountMax.Amount.IsNegative() {
		return NewError(errors.ErrInvalid, "amount max: must not be negative")
	}
	if f.AmountMin.Valid && f.AmountMax.Valid && f.AmountMin.Amount.Cmp(f.AmountMax.Amount) > 0 {
		return NewError(errors.ErrInvalid, "amount min: must not be above max")
	}
	return nil
}

// TypeSum is the total of operations of one type in a currency.
type TypeSum struct {
	Type     OpType
	Currency string
	Total    money.Amount
}

// CashFlow is income and expense in one currency.
type CashFlow struct {
	Currency string
	Income   money.Amount
	Expense  money.Amount
}

// Net returns income minus expense.
func (c CashFlow) Net() money.Amount {
	return c.Income.Sub(c.Expense)
}

// CashFlows returns income and expense of sums per currency, ordered by currency.
// Transfers and adjustments are not income or expense and are skipped.
func CashFlows(sums []TypeSum) []CashFlow {
	var out []CashFlow
	for _, s := range sums {
		if s.Type != OpIncome && s.Type != OpExpense {
			continue
		}
		i := slices.IndexFunc(out, func(c CashFlow) bool { return c.Currency == s.Currency })
		if i < 0 {
			i = len(out)
			out = append(out, CashFlow{Currency: s.Currency})
		}
		if s.Type == OpIncome {
			out[i].Income = out[i].Income.Add(s.Total)
		} else {
			out[i].Expense = out[i].Expense.Add(s.Total)
		}
	}
	slices.SortFunc(out, func(a, b CashFlow) int { return strings.Compare(a.Currency, b.Currency) })
	return out
}
