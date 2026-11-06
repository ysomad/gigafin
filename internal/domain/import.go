package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// ImportSource is the format of an imported file.
type ImportSource string

const (
	// ImportSourceGigaFin is the gigafin CSV format, see Record.
	ImportSourceGigaFin  ImportSource = "gigafin"
	ImportSourceTBank    ImportSource = "tbank"
	ImportSourceZenMoney ImportSource = "zenmoney"
)

// ImportSources returns all import sources.
func ImportSources() []ImportSource {
	return []ImportSource{ImportSourceGigaFin, ImportSourceTBank, ImportSourceZenMoney}
}

// ParseImportSource validates an import source string.
func ParseImportSource(s string) (ImportSource, error) {
	src := ImportSource(s)
	if !slices.Contains(ImportSources(), src) {
		return "", NewFieldError(errors.ErrInvalid, "source", "must be gigafin, tbank or zenmoney")
	}
	return src, nil
}

// ImportStatus is the state of an import.
type ImportStatus string

const (
	ImportStaged    ImportStatus = "staged"
	ImportCommitted ImportStatus = "committed"
	ImportUndone    ImportStatus = "undone"
)

// ImportRowStatus is the state of an import row.
type ImportRowStatus string

const (
	ImportRowNew       ImportRowStatus = "new"
	ImportRowDuplicate ImportRowStatus = "duplicate"
	ImportRowSkipped   ImportRowStatus = "skipped"
)

// _importNotReady is the error message of committing an import that is not fully mapped.
const _importNotReady = "map all accounts and categories first"

// Record is one operation of an imported file in the gigafin import format: every
// source is parsed into records. It is stored as JSON in staged rows, so the tags must
// stay stable.
type Record struct {
	Line    int    `json:"line"`
	Account string `json:"account"`
	// ToAccount makes the record a transfer from Account to ToAccount in the same
	// currency. Transfers without it are paired by PairTransfers.
	ToAccount string    `json:"to_account,omitempty"`
	Time      time.Time `json:"time"`
	// DateOnly is set when the file has no time of the operation: only the date of Time
	// in the user time zone counts.
	DateOnly bool `json:"date_only,omitempty"`
	// Amount is signed in the account currency: negative is money out. A transfer is
	// negative.
	Amount   money.Amount `json:"amount"`
	Currency string       `json:"currency"`
	// Category is the source category name, empty for transfers.
	Category string `json:"category"`
	// Group is set on records of one file that are the parts of one split operation;
	// empty when the record is not split. Transfers are not split.
	Group string `json:"group,omitempty"`
	Payee string `json:"description"`
	Note  string `json:"message,omitempty"`
	// MCC is the Merchant Category Code of a card payment, empty when unknown.
	MCC string `json:"mcc,omitempty"`
	// Tags are tag names, validated when the record is committed.
	Tags []string `json:"tags,omitempty"`
	// SkipReason is set on rows that are not imported.
	SkipReason string `json:"skip_reason,omitempty"`
}

// ParseResult is a parsed file.
type ParseResult struct {
	Rows []Record
	// Skipped are records that are not imported, with SkipReason set. They are parsed
	// leniently: fields that do not parse are left empty.
	Skipped []Record
}

// IsTransfer reports whether the record is a transfer between own accounts.
func (r Record) IsTransfer() bool {
	return r.ToAccount != ""
}

// Kind returns the category kind of the row when it is not a transfer.
func (r Record) Kind() Kind {
	if r.Amount.IsNegative() {
		return KindExpense
	}
	return KindIncome
}

// Hash identifies a row across uploads. n is the occurrence of identical rows in the
// file, so two equal purchases in one export both import.
func (r Record) Hash(src ImportSource, n int) string {
	// T-Bank times are instants. Other sources give the wall clock of the user, so a
	// change of the user time zone must not change their hashes.
	at := r.Time.UTC().Format(time.RFC3339)
	if src != ImportSourceTBank {
		at = r.Time.Format(time.DateTime)
	}
	parts := []string{
		string(src),
		r.Account,
		at,
		r.Amount.String(),
		r.Currency,
		r.Payee,
		strconv.Itoa(n),
	}
	// appended only when set, so hashes of rows imported before it existed stay the same
	if r.ToAccount != "" {
		parts = append(parts, r.ToAccount)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:16])
}

// HashRows hashes rows of one file, counting occurrences of identical rows. Identical
// rows of overlapping exports concatenated into one file count as distinct too, as
// they cannot be told apart from equal purchases.
func HashRows(src ImportSource, rows []Record) []string {
	seen := make(map[string]int, len(rows))
	hashes := make([]string, len(rows))
	for i, r := range rows {
		base := r.Hash(src, 0)
		hashes[i] = r.Hash(src, seen[base])
		seen[base]++
	}
	return hashes
}

// Import is one uploaded file.
type Import struct {
	ID        int64
	Source    ImportSource
	Filename  string
	Status    ImportStatus
	RowsTotal int64
	RowsNew   int64
	RowsDup   int64
	RowsSkip  int64
	CreatedAt time.Time
}

// CheckStatus fails with errors.ErrConflict unless the import is in want.
func (i Import) CheckStatus(want ImportStatus) error {
	if i.Status != want {
		return NewError(errors.ErrConflict, "import is %s", Msg(i.Status))
	}
	return nil
}

// CheckDiscard fails with errors.ErrConflict when the import is committed: its
// operations must be undone first.
func (i Import) CheckDiscard() error {
	if i.Status == ImportCommitted {
		return NewError(errors.ErrConflict, "undo the import first")
	}
	return nil
}

// ImportRow is a staged row.
type ImportRow struct {
	ID     int64
	Hash   string
	Row    Record
	Status ImportRowStatus
	// PairID is the row on the other side of a transfer, 0 if unpaired.
	PairID int64
	// OperationID is set once committed.
	OperationID int64
}

// StageImportRows returns im with the row counts of a parsed export and the rows to
// stage. rows are hashed by HashRows into hashes and are duplicates when imported
// before; skipped rows are not imported.
func StageImportRows(
	im Import,
	rows, skipped []Record,
	hashes []string,
	imported map[string]bool,
) (Import, []ImportRow) {
	staged := make([]ImportRow, len(rows)+len(skipped))
	for i, rec := range rows {
		row := ImportRow{Hash: hashes[i], Row: rec, Status: ImportRowNew}
		if imported[row.Hash] {
			row.Status = ImportRowDuplicate
			im.RowsDup++
		}
		staged[i] = row
	}
	for i, rec := range skipped {
		staged[len(rows)+i] = ImportRow{
			Hash: rec.Hash(im.Source, 0), Row: rec, Status: ImportRowSkipped,
		}
	}
	im.RowsTotal = int64(len(rows))
	im.RowsNew = im.RowsTotal - im.RowsDup
	im.RowsSkip = int64(len(skipped))
	return im, staged
}

// PairTransfers matches new money out of one account with the same amount into
// another account of the same currency within two minutes. Records that are
// transfers already, parts of a split or without a time are left out: the time of a
// date-only record is midnight, so any same-day expense and income of equal amounts
// would pair. It returns [out, in] row ids.
func PairTransfers(rows []ImportRow) [][2]int64 {
	// how far apart the two sides of a transfer between own accounts may be
	const window = 2 * time.Minute
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b ImportRow) int { return a.Row.Time.Compare(b.Row.Time) })

	used := make(map[int64]bool, len(rows))
	var pairs [][2]int64
	pairable := func(r ImportRow) bool {
		return r.Status == ImportRowNew && !used[r.ID] && !r.Row.IsTransfer() && r.Row.Group == "" &&
			!r.Row.DateOnly
	}
	for _, out := range sorted {
		if !pairable(out) || !out.Row.Amount.IsNegative() {
			continue
		}
		var (
			best    = -1
			bestGap time.Duration
		)
		for j, in := range sorted {
			if !pairable(in) || !in.Row.Amount.IsPositive() ||
				in.Row.Account == out.Row.Account || in.Row.Currency != out.Row.Currency ||
				!in.Row.Amount.Equal(out.Row.Amount.Neg()) {
				continue
			}
			gap := in.Row.Time.Sub(out.Row.Time).Abs()
			if gap > window {
				continue
			}
			if best < 0 || gap < bestGap {
				best, bestGap = j, gap
			}
		}
		if best >= 0 {
			used[out.ID], used[sorted[best].ID] = true, true
			pairs = append(pairs, [2]int64{out.ID, sorted[best].ID})
		}
	}
	return pairs
}

// AccountKindRule gives bank accounts whose name matches Pattern the kind Kind.
type AccountKindRule struct {
	Kind    AccountKind
	Pattern *regexp.Regexp
}

// GuessAccountKind returns the kind of the first rule matching name, or debit when
// none matches.
func GuessAccountKind(name string, rules []AccountKindRule) AccountKind {
	for _, r := range rules {
		if r.Pattern.MatchString(name) {
			return r.Kind
		}
	}
	return AccountDebit
}

// AccountNeed is a bank account found in the file and the gigafin account it maps to.
type AccountNeed struct {
	Name     string
	Currency string
	// AccountID is 0 when unmapped.
	AccountID int64
	// Kind is the guessed kind for a new account.
	Kind AccountKind
	Rows int
}

// CategoryNeed is a bank category used by unpaired rows in one direction and the
// category it maps to.
type CategoryNeed struct {
	Name string
	Kind Kind
	// CategoryID is 0 when unmapped.
	CategoryID int64
	Rows       int
}

// AccountKey identifies a bank account in a currency: one bank account name can hold
// several currencies.
func AccountKey(name, currency string) string {
	return name + "\x1f" + currency
}

// CategoryKey identifies a bank category of a kind.
func CategoryKey(kind Kind, name string) string {
	return string(kind) + ":" + name
}

// ImportMapping maps bank account and category keys to own accounts and categories.
type ImportMapping struct {
	// Accounts is keyed by AccountKey.
	Accounts map[string]Account
	// Categories is keyed by CategoryKey.
	Categories map[string]Category
}

// Preview is a staged import with what is still needed to commit it.
type Preview struct {
	Import     Import
	Accounts   []AccountNeed
	Categories []CategoryNeed
	Rows       []ImportRow
	// Skipped are rows the parser did not import, see Record.SkipReason.
	Skipped []ImportRow
}

// Suggest returns p with unmapped accounts and categories mapped to own ones of the
// same name: a not archived account of the same currency, or a category of the same
// kind by its full name. accounts are the own accounts and tree the category tree.
// Suggestions are only shown; they apply once the mapping is saved.
func (p Preview) Suggest(accounts []Account, tree []Category) Preview {
	p.Accounts = slices.Clone(p.Accounts)
	for i, need := range p.Accounts {
		if need.AccountID != 0 {
			continue
		}
		for _, a := range accounts {
			if !a.Archived && a.Name == need.Name && a.Currency == need.Currency {
				p.Accounts[i].AccountID = a.ID
				break
			}
		}
	}
	cats := make(map[string]int64)
	for _, c1 := range tree {
		if !c1.Archived {
			cats[CategoryKey(c1.Kind, c1.Name)] = c1.ID
		}
		for _, c2 := range c1.Children {
			if !c2.Archived {
				cats[CategoryKey(c1.Kind, FullName(c1.Name, c2.Name))] = c2.ID
			}
		}
	}
	p.Categories = slices.Clone(p.Categories)
	for i, need := range p.Categories {
		if need.CategoryID == 0 {
			p.Categories[i].CategoryID = cats[CategoryKey(need.Kind, need.Name)]
		}
	}
	return p
}

// AccountsMapped reports whether every account is mapped.
func (p Preview) AccountsMapped() bool {
	for _, a := range p.Accounts {
		if a.AccountID == 0 {
			return false
		}
	}
	return true
}

// CategoriesMapped reports whether every category is mapped.
func (p Preview) CategoriesMapped() bool {
	for _, c := range p.Categories {
		if c.CategoryID == 0 {
			return false
		}
	}
	return true
}

// Ready reports whether every account and category is mapped.
func (p Preview) Ready() bool {
	return p.AccountsMapped() && p.CategoriesMapped()
}

// CheckReady fails with errors.ErrConflict unless the preview is Ready.
func (p Preview) CheckReady() error {
	if !p.Ready() {
		return NewError(errors.ErrConflict, _importNotReady)
	}
	return nil
}

// NewPreview lists the accounts and categories new rows of im need and how they
// map. rules guess the kind of accounts to create.
func NewPreview(im Import, rows []ImportRow, m ImportMapping, rules []AccountKindRule) Preview {
	p := Preview{Import: im}
	for _, r := range rows {
		if r.Status == ImportRowSkipped {
			p.Skipped = append(p.Skipped, r)
		} else {
			p.Rows = append(p.Rows, r)
		}
	}
	accIdx := make(map[string]int)
	catIdx := make(map[string]int)
	for _, r := range p.Rows {
		if r.Status != ImportRowNew {
			continue
		}
		names := []string{r.Row.Account}
		if r.Row.IsTransfer() {
			names = append(names, r.Row.ToAccount)
		}
		for _, name := range names {
			key := AccountKey(name, r.Row.Currency)
			i, ok := accIdx[key]
			if !ok {
				need := AccountNeed{
					Name:     name,
					Currency: r.Row.Currency,
					Kind:     GuessAccountKind(name, rules),
				}
				if a, ok := m.Accounts[key]; ok && a.Currency == r.Row.Currency {
					need.AccountID = a.ID
				}
				i = len(p.Accounts)
				accIdx[key] = i
				p.Accounts = append(p.Accounts, need)
			}
			p.Accounts[i].Rows++
		}

		if r.PairID != 0 || r.Row.IsTransfer() {
			continue
		}
		ck := CategoryKey(r.Row.Kind(), r.Row.Category)
		j, ok := catIdx[ck]
		if !ok {
			need := CategoryNeed{Name: r.Row.Category, Kind: r.Row.Kind()}
			if c, ok := m.Categories[ck]; ok && c.Kind == need.Kind {
				need.CategoryID = c.ID
			}
			j = len(p.Categories)
			catIdx[ck] = j
			p.Categories = append(p.Categories, need)
		}
		p.Categories[j].Rows++
	}
	slices.SortFunc(p.Categories, func(a, b CategoryNeed) int {
		return strings.Compare(CategoryKey(a.Kind, a.Name), CategoryKey(b.Kind, b.Name))
	})
	return p
}

// RowCommit is the operation to create for a new import row. Pair is the incoming side
// of a transfer, nil for other rows.
type RowCommit struct {
	Row  ImportRow
	Pair *ImportRow
	Op   OperationInput
	// Split is set on the parts of a split operation, the commits sharing Row.Row.Group,
	// when there are at least two of them. They share the id of the first as origin.
	Split bool
}

// CommitRows returns the operations to create for new rows that are not in imported.
// A transfer is created once, from its outgoing side. Dates and times are converted to
// loc. It fails with errors.ErrConflict when a row is not mapped or the incoming side
// of a transfer was imported since, so its outgoing side would be lost.
func CommitRows(rows []ImportRow, imported map[string]bool, m ImportMapping, loc *time.Location) ([]RowCommit, error) {
	byID := make(map[int64]ImportRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	var commits []RowCommit
	for _, row := range rows {
		if row.Status != ImportRowNew || imported[row.Hash] {
			continue
		}
		c := RowCommit{Row: row}
		if p, ok := byID[row.PairID]; ok {
			if row.Row.Amount.IsPositive() {
				continue // created with its outgoing side
			}
			if imported[p.Hash] {
				return nil, NewError(
					errors.ErrConflict,
					"line %d: the other side of the transfer was imported since, unpair it",
					row.Row.Line,
				)
			}
			c.Pair = &p
		}
		var err error
		if c.Op, err = rowOperation(row, c.Pair, m, loc); err != nil {
			return nil, err
		}
		commits = append(commits, c)
	}
	parts := make(map[string]int)
	for _, c := range commits {
		if g := c.Row.Row.Group; g != "" {
			parts[g]++
		}
	}
	for i, c := range commits {
		commits[i].Split = parts[c.Row.Row.Group] > 1
	}
	return commits, nil
}

// rowOperation returns the operation for a new row r, or for a transfer whose outgoing
// side is r and incoming side is pair.
func rowOperation(r ImportRow, pair *ImportRow, m ImportMapping, loc *time.Location) (OperationInput, error) {
	notReady := NewError(errors.ErrConflict, _importNotReady)
	account := func(name string) (int64, bool) {
		a, ok := m.Accounts[AccountKey(name, r.Row.Currency)]
		return a.ID, ok
	}
	accountID, ok := account(r.Row.Account)
	if !ok {
		return OperationInput{}, notReady
	}
	t := r.Row.Time.In(loc)
	in := OperationInput{
		AccountID:   accountID,
		Amount:      r.Row.Amount.Abs(),
		Date:        timex.Date(t),
		Payee:       r.Row.Payee,
		SourcePayee: r.Row.Payee,
		Note:        r.Row.Note,
		MCC:         r.Row.MCC,
		Tags:        r.Row.Tags,
	}
	if !r.Row.DateOnly {
		in.Time = timex.FormatClock(t)
	}
	if r.Row.IsTransfer() || pair != nil {
		to := r.Row.ToAccount
		if pair != nil {
			to = pair.Row.Account
		}
		in.Type = OpTransfer
		if in.ToAccountID, ok = account(to); !ok {
			return OperationInput{}, notReady
		}
		return in, nil
	}
	in.Type = OpExpense
	if r.Row.Kind() == KindIncome {
		in.Type = OpIncome
	}
	cat, ok := m.Categories[CategoryKey(r.Row.Kind(), r.Row.Category)]
	if !ok {
		return OperationInput{}, notReady
	}
	in.CategoryID = cat.ID
	return in, nil
}

// AccountChoice maps a bank account to an existing account or creates one.
type AccountChoice struct {
	Name     string
	Currency string
	// AccountID maps to an existing account of the same currency.
	AccountID int64
	// NewName creates an account of NewKind when AccountID is 0.
	NewName string
	NewKind AccountKind
	// NewCreditLimit is the limit of a new credit account, zero when not set.
	NewCreditLimit money.Amount
}

// CheckAccount validates mapping the bank account to a.
func (c AccountChoice) CheckAccount(a Account) error {
	if a.Currency != c.Currency {
		return NewError(
			errors.ErrInvalid,
			"%s: %q is a %s account, the bank account is %s",
			c.Name,
			a.Name,
			a.Currency,
			c.Currency,
		)
	}
	return nil
}

// CategoryChoice maps a bank category of a kind to an existing category or creates one.
type CategoryChoice struct {
	Name string
	Kind Kind
	// CategoryID maps to an existing category of the same kind.
	CategoryID int64
	// Cat1 and the optional Cat2 create "Cat1 / Cat2" or just "Cat1" when CategoryID
	// is 0. Missing levels are created, existing ones reused.
	Cat1 string
	Cat2 string
}

// CheckCategory validates mapping the bank category to c.
func (ch CategoryChoice) CheckCategory(c Category) error {
	if c.Kind != ch.Kind {
		return NewError(errors.ErrInvalid, "%s: pick an %s category", ch.Name, Msg(ch.Kind))
	}
	return nil
}
