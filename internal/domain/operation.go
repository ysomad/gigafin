package domain

import (
	"cmp"
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

// Kind returns the kind of the categories of an operation of type t, false for a
// transfer or an adjustment, which have no category.
func (t OpType) Kind() (Kind, bool) {
	switch t {
	case OpExpense:
		return KindExpense, true
	case OpIncome:
		return KindIncome, true
	case OpTransfer, OpAdjustment:
	}
	return "", false
}

// IsCategorized reports whether an operation of type t, an expense or income, has a
// category.
func (t OpType) IsCategorized() bool {
	_, ok := t.Kind()
	return ok
}

// CheckAmount checks the amount a of an operation of type t: the balance change of an
// adjustment, signed, is not zero, the amount of the others is positive.
func (t OpType) CheckAmount(a money.Amount) error {
	if t == OpAdjustment {
		if a.IsZero() {
			return NewError(errors.ErrInvalid, "must not be zero")
		}
		return nil
	}
	if !a.IsPositive() {
		return NewError(errors.ErrInvalid, "must be positive")
	}
	return nil
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
	// CategoryID is the category of an expense or income, 0 for one without a category
	// and for transfers and adjustments.
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

// Equal reports whether o and other are the same operation with the same data, when
// and how they were created aside.
func (o Operation) Equal(other Operation) bool {
	return o.ID == other.ID && o.Type == other.Type && o.AccountID == other.AccountID &&
		o.ToAccountID == other.ToAccountID && o.CategoryID == other.CategoryID && o.OriginID == other.OriginID &&
		o.Amount.Equal(other.Amount) && o.Date.Equal(other.Date) && o.Time == other.Time &&
		o.Payee == other.Payee && o.Note == other.Note && slices.Equal(o.Tags, other.Tags)
}

// IsSplit reports whether the operation is a part of a split.
func (o Operation) IsSplit() bool {
	return o.OriginID != 0
}

// IsSplitOrigin reports whether the operation is the origin of its split, the
// operation the split was made from.
func (o Operation) IsSplitOrigin() bool {
	return o.OriginID != 0 && o.ID == o.OriginID
}

// SourceDelta returns the change the operation makes to the balance of its account:
// negative for money out, an expense or a transfer, signed for an adjustment.
func (o Operation) SourceDelta() money.Amount {
	if o.Type == OpExpense || o.Type == OpTransfer {
		return o.Amount.Neg()
	}
	return o.Amount
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
	// CategoryID is the category of an expense or income, 0 for none; it must be 0 for
	// transfers and adjustments.
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
	if err := typ.CheckAmount(in.Amount); err != nil {
		return Operation{}, NewError(err, "amount")
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
		return nil
	}
	if cat == nil || cat.ID != in.CategoryID {
		return NewError(errors.ErrInvalid, "category: category not found")
	}
	if cat.Kind != o.Kind() {
		return NewError(errors.ErrInvalid, "category: category %q is not an %s category", cat.Name, Msg(o.Kind()))
	}
	o.CategoryID = cat.ID
	return nil
}

// SplitPart is one part an expense or income is split into.
type SplitPart struct {
	// CategoryID is the category of the part, 0 leaves it uncategorized.
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
	if !op.Type.IsCategorized() {
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
		// 0 leaves the part uncategorized
		if p.CategoryID != 0 {
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
		part.CategoryID, part.Amount, part.OriginID = p.CategoryID, p.Amount, origin
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

// UnsplitOperation merges the parts of a split back into one operation and returns it:
// the origin of the split, or the earliest part when the origin is deleted, with its
// category, the amount and tags of all parts, their distinct notes joined after its
// own, and out of the split. Parts must keep the type and account of the split.
func UnsplitOperation(parts []Operation) (Operation, error) {
	if len(parts) == 0 {
		return Operation{}, NewError(errors.ErrNotFound, "split not found")
	}
	// the kept part first, the others in the order they were created
	parts = slices.Clone(parts)
	slices.SortFunc(parts, func(a, b Operation) int {
		switch {
		case a.IsSplitOrigin() && !b.IsSplitOrigin():
			return -1
		case !a.IsSplitOrigin() && b.IsSplitOrigin():
			return 1
		}
		return cmp.Compare(a.ID, b.ID)
	})
	op := parts[0]
	var (
		sum   money.Amount
		notes []string
		tags  []string
	)
	for _, p := range parts {
		if p.Type != op.Type || p.AccountID != op.AccountID {
			return Operation{}, NewError(errors.ErrInvalid, "parts differ in type or account")
		}
		sum = sum.Add(p.Amount)
		if p.Note != "" && !slices.Contains(notes, p.Note) {
			notes = append(notes, p.Note)
		}
		for _, t := range p.Tags {
			if !slices.ContainsFunc(tags, func(k string) bool { return strings.EqualFold(k, t) }) {
				tags = append(tags, t)
			}
		}
	}
	op.Note = strings.Join(notes, "; ")
	if utf8.RuneCountInString(op.Note) > MaxTextLen {
		return Operation{}, NewError(errors.ErrInvalid, "note: notes of the parts are too long together")
	}
	if len(tags) > MaxTags {
		return Operation{}, NewError(errors.ErrInvalid, "tags: parts have more than %d together", MaxTags)
	}
	slices.Sort(tags)
	op.Amount, op.OriginID, op.Tags = sum, 0, tags
	return op, nil
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

// OpSort is an order of listed operations, SortNewestFirst when empty.
type OpSort string

const (
	// SortNewestFirst lists the newest first, by date and time, parts of a split together.
	SortNewestFirst OpSort = "newest_first"
	// SortOldestFirst lists the oldest first.
	SortOldestFirst OpSort = "oldest_first"
	// SortLargestFirst lists the largest absolute amount in minor units first, whatever
	// the currency, then by date as SortNewestFirst.
	SortLargestFirst OpSort = "largest_first"
	// SortSmallestFirst lists the least absolute amount in minor units first, then by date
	// as SortOldestFirst.
	SortSmallestFirst OpSort = "smallest_first"
)

// OpSorts returns all orders of listed operations.
func OpSorts() []OpSort {
	return []OpSort{SortNewestFirst, SortOldestFirst, SortLargestFirst, SortSmallestFirst}
}

// ByAmount reports whether s orders by absolute amount first, else by date.
func (s OpSort) ByAmount() bool { return s == SortLargestFirst || s == SortSmallestFirst }

// IsAscending reports whether s lists the least first.
func (s OpSort) IsAscending() bool { return s == SortOldestFirst || s == SortSmallestFirst }

// Filter selects operations. Zero fields match everything.
type Filter struct {
	AccountID int64
	// CategoryIDs are categories, a root one with its nested ones; an operation
	// matches any of them.
	CategoryIDs []int64
	// Uncategorized selects expenses and income without a category too.
	Uncategorized bool
	Type          OpType
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
	// Sort orders the listed operations. It selects none, so IsFiltered and Count skip it.
	Sort OpSort
}

// IsFiltered reports whether any field is set.
func (f Filter) IsFiltered() bool {
	return f.AccountID != 0 || len(f.CategoryIDs) > 0 || f.Uncategorized || f.Type != "" || !f.DateFrom.IsZero() ||
		!f.DateTo.IsZero() || f.Query != "" || f.AmountMin.Valid || f.AmountMax.Valid || len(f.Tags) > 0
}

// Count returns the number of fields set, but Query.
func (f Filter) Count() int {
	var n int
	for _, set := range []bool{
		f.AccountID != 0, len(f.CategoryIDs) > 0 || f.Uncategorized, f.Type != "", !f.DateFrom.IsZero(), !f.DateTo.IsZero(),
		f.AmountMin.Valid, f.AmountMax.Valid, len(f.Tags) > 0,
	} {
		if set {
			n++
		}
	}
	return n
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
	if f.Sort != "" && !slices.Contains(OpSorts(), f.Sort) {
		return NewError(errors.ErrInvalid, "sort: must be newest_first, oldest_first, largest_first or smallest_first")
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
		if !s.Type.IsCategorized() {
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
