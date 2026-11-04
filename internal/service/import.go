package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Imports stages files of every source parsed into records, maps their accounts and categories, pairs transfers and
// commits them as operations. Committed imports can be undone.
type Imports struct {
	store       *repository.Store
	clock       timex.Clock
	log         *slog.Logger
	kindRules   []domain.AccountKindRule
	categorizer Categorizer
}

// ImportOptions tune imports.
type ImportOptions struct {
	// Regular expressions matched against bank account names that pick the kind of a
	// new account, tried in the order loan, credit, cash, savings, debit so a car loan
	// named "Автокредит" is not a card; no match is debit. Empty disables the kind.
	LoanPattern    string
	CreditPattern  string
	CashPattern    string
	SavingsPattern string
	DebitPattern   string
}

func newImports(store *repository.Store, clock timex.Clock, log *slog.Logger, opts ImportOptions) (*Imports, error) {
	s := &Imports{store: store, clock: clock, log: log}
	for _, p := range [...]struct {
		kind    domain.AccountKind
		name    string
		pattern string
	}{
		{kind: domain.AccountLoan, name: "loan", pattern: opts.LoanPattern},
		{kind: domain.AccountCredit, name: "credit", pattern: opts.CreditPattern},
		{kind: domain.AccountCash, name: "cash", pattern: opts.CashPattern},
		{kind: domain.AccountSavings, name: "savings", pattern: opts.SavingsPattern},
		{kind: domain.AccountDebit, name: "debit", pattern: opts.DebitPattern},
	} {
		if p.pattern == "" {
			continue
		}
		re, err := regexp.Compile(p.pattern)
		if err != nil {
			return nil, fmt.Errorf("%s account pattern: %w", p.name, err)
		}
		s.kindRules = append(s.kindRules, domain.AccountKindRule{Kind: p.kind, Pattern: re})
	}
	return s, nil
}

// Header returns the header line of a file of src: the columns Upload reads joined by
// the separator of the format.
func (s *Imports) Header(src domain.ImportSource) string {
	return importer.Header(src)
}

// Upload parses a file of src and stages it. Dates and times without a zone are read
// in loc. Rows imported before are marked as duplicates and transfers between own
// accounts are paired.
func (s *Imports) Upload(
	ctx context.Context, userID int64, src domain.ImportSource, filename string, file io.Reader, loc *time.Location,
) (domain.Import, error) {
	res, err := importer.Parse(src, file, loc)
	if err != nil {
		return domain.Import{}, err
	}
	hashes := domain.HashRows(src, res.Rows)

	var im domain.Import
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		imported, err := r.Imports.ImportedHashes(ctx, userID, hashes)
		if err != nil {
			return err
		}
		if im, err = r.Imports.Create(ctx, userID, src, filename, s.clock.Now()); err != nil {
			return err
		}
		var staged []domain.ImportRow
		im, staged = domain.StageImportRows(im, res.Rows, res.Skipped, hashes, imported)
		for i := range staged {
			if staged[i].ID, err = r.Imports.CreateRow(ctx, im.ID, staged[i]); err != nil {
				return err
			}
		}
		for _, p := range domain.PairTransfers(staged) {
			if err := r.Imports.SetPair(ctx, p[0], p[1]); err != nil {
				return err
			}
			if err := r.Imports.SetPair(ctx, p[1], p[0]); err != nil {
				return err
			}
		}
		return r.Imports.Update(ctx, userID, im)
	})
	if err != nil {
		return im, err
	}
	s.log.InfoContext(ctx, "import uploaded", slog.Int64("user_id", userID), slog.Int64("import_id", im.ID),
		slog.String("source", string(src)), slog.Int64("rows", im.RowsTotal), slog.Int64("rows_new", im.RowsNew),
		slog.Int64("rows_dup", im.RowsDup), slog.Int64("rows_skip", im.RowsSkip))
	return im, nil
}

// List returns recent imports, newest first.
func (s *Imports) List(ctx context.Context, userID int64) ([]domain.Import, error) {
	return s.store.Read().Imports.List(ctx, userID)
}

// Preview returns the import rows and the mapping state.
func (s *Imports) Preview(ctx context.Context, userID, importID int64) (domain.Preview, error) {
	im, rows, m, err := loadImport(ctx, s.store.Read(), userID, importID)
	if err != nil {
		return domain.Preview{}, err
	}
	return domain.NewPreview(im, rows, m, s.kindRules), nil
}

// SaveMapping stores account and category mappings. They apply to later imports too.
func (s *Imports) SaveMapping(
	ctx context.Context, userID, importID int64, accounts []domain.AccountChoice, categories []domain.CategoryChoice,
) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		im, err := r.Imports.Get(ctx, userID, importID)
		if err != nil {
			return err
		}
		if err := im.CheckStatus(domain.ImportStaged); err != nil {
			return err
		}
		now := s.clock.Now()
		for _, c := range accounts {
			if err := mapAccount(ctx, r, userID, im.Source, c, now); err != nil {
				return err
			}
		}
		for _, c := range categories {
			if err := mapCategory(ctx, r, userID, im.Source, c, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "import mapping saved", slog.Int64("user_id", userID), slog.Int64("import_id", importID),
		slog.Int("accounts", len(accounts)), slog.Int("categories", len(categories)))
	return nil
}

// Unpair splits a paired transfer back into an expense and an income.
func (s *Imports) Unpair(ctx context.Context, userID, importID, rowID int64) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		im, err := r.Imports.Get(ctx, userID, importID)
		if err != nil {
			return err
		}
		if err := im.CheckStatus(domain.ImportStaged); err != nil {
			return err
		}
		rows, err := r.Imports.ListRows(ctx, importID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(rows, func(row domain.ImportRow) bool { return row.ID == rowID })
		if i < 0 || rows[i].PairID == 0 {
			return fmt.Errorf("paired row: %w", errors.ErrNotFound)
		}
		if err := r.Imports.SetPair(ctx, rowID, 0); err != nil {
			return err
		}
		return r.Imports.SetPair(ctx, rows[i].PairID, 0)
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "import transfer unpaired", slog.Int64("user_id", userID),
		slog.Int64("import_id", importID), slog.Int64("row_id", rowID))
	return nil
}

// Commit creates operations for all new rows of a staged import. Dates and times are
// converted to loc. Rules run on every row that is not a part of a split and override
// the mapped category. It returns the number of operations created.
//
//nolint:cyclop // its read and write phases check the same state, so they stay together
func (s *Imports) Commit(ctx context.Context, userID, importID int64, loc *time.Location) (int, error) {
	// rows are prepared and categorized outside the write transaction, as a categorizer
	// may be slow; the transaction checks nothing changed meanwhile
	im, rows, m, err := loadImport(ctx, s.store.Read(), userID, importID)
	if err != nil {
		return 0, err
	}
	if err := im.CheckStatus(domain.ImportStaged); err != nil {
		return 0, err
	}
	if err := domain.NewPreview(im, rows, m, s.kindRules).CheckReady(); err != nil {
		return 0, err
	}
	// rows were deduplicated at upload; check again in case an import with the same
	// rows was committed since
	hashes := make([]string, len(rows))
	for i, row := range rows {
		hashes[i] = row.Hash
	}
	imported, err := s.store.Read().Imports.ImportedHashes(ctx, userID, hashes)
	if err != nil {
		return 0, err
	}
	commits, err := domain.CommitRows(rows, imported, m, loc)
	if err != nil {
		return 0, err
	}
	if err := s.categorize(ctx, userID, commits); err != nil {
		return 0, err
	}

	var created int
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		im, err := r.Imports.Get(ctx, userID, importID)
		if err != nil {
			return err
		}
		if err := im.CheckStatus(domain.ImportStaged); err != nil {
			return err
		}
		again, err := r.Imports.ImportedHashes(ctx, userID, hashes)
		if err != nil {
			return err
		}
		if len(again) != len(imported) {
			return domain.NewError(errors.ErrConflict, "rows of the import were imported meanwhile, try again")
		}

		// split group to the id of its first operation, the origin of all its parts
		origins := make(map[string]int64)
		now := s.clock.Now()
		for _, c := range commits {
			ref := domain.ImportRef{ImportID: importID, Hash: c.Row.Hash}
			op, err := createOperation(ctx, r, userID, c.Op, ref, now)
			if err != nil {
				return fmt.Errorf("line %d: %w", c.Row.Row.Line, err)
			}
			if err := r.Imports.SetOperation(ctx, c.Row.ID, op.ID); err != nil {
				return err
			}
			if c.Split {
				g := c.Row.Row.Group
				if _, ok := origins[g]; !ok {
					origins[g] = op.ID
				}
				op.OriginID = origins[g]
				if _, err := r.Operations.Update(ctx, userID, op, now); err != nil {
					return err
				}
			}
			if c.Pair != nil {
				if err := r.Imports.SetOperation(ctx, c.Pair.ID, op.ID); err != nil {
					return err
				}
			}
		}
		created = len(commits)
		im.Status = domain.ImportCommitted
		return r.Imports.Update(ctx, userID, im)
	})
	if err != nil {
		return created, err
	}
	s.log.InfoContext(ctx, "import committed", slog.Int64("user_id", userID), slog.Int64("import_id", importID),
		slog.Int("created", created))
	return created, nil
}

// categorize replaces the operations of commits that are not parts of splits with
// what the categorizer makes of them.
func (s *Imports) categorize(ctx context.Context, userID int64, commits []domain.RowCommit) error {
	var (
		ins []domain.OperationInput
		idx []int
	)
	for i, c := range commits {
		if !c.Split {
			ins = append(ins, c.Op)
			idx = append(idx, i)
		}
	}
	ruled, err := s.categorizer.Categorize(ctx, userID, ins)
	if err != nil {
		return err
	}
	for i, in := range ruled {
		commits[idx[i]].Op = in
	}
	return nil
}

// Undo deletes the operations a committed import created, so its rows can be imported
// again. It returns the number of operations deleted.
func (s *Imports) Undo(ctx context.Context, userID, importID int64) (int64, error) {
	var n int64
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		im, err := r.Imports.Get(ctx, userID, importID)
		if err != nil {
			return err
		}
		if err := im.CheckStatus(domain.ImportCommitted); err != nil {
			return err
		}
		if n, err = r.Operations.DeleteImport(ctx, userID, importID); err != nil {
			return err
		}
		im.Status = domain.ImportUndone
		return r.Imports.Update(ctx, userID, im)
	})
	if err != nil {
		return n, err
	}
	s.log.InfoContext(ctx, "import undone", slog.Int64("user_id", userID), slog.Int64("import_id", importID),
		slog.Int64("deleted", n))
	return n, nil
}

// Discard deletes an import that is not committed.
func (s *Imports) Discard(ctx context.Context, userID, importID int64) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		im, err := r.Imports.Get(ctx, userID, importID)
		if err != nil {
			return err
		}
		if err := im.CheckDiscard(); err != nil {
			return err
		}
		return r.Imports.Delete(ctx, userID, importID)
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "import discarded", slog.Int64("user_id", userID), slog.Int64("import_id", importID))
	return nil
}

func loadImport(
	ctx context.Context, r repository.Repos, userID, importID int64,
) (domain.Import, []domain.ImportRow, domain.ImportMapping, error) {
	im, err := r.Imports.Get(ctx, userID, importID)
	if err != nil {
		return domain.Import{}, nil, domain.ImportMapping{}, err
	}
	rows, err := r.Imports.ListRows(ctx, importID)
	if err != nil {
		return domain.Import{}, nil, domain.ImportMapping{}, err
	}
	m, err := r.Imports.Mapping(ctx, userID, im.Source)
	if err != nil {
		return domain.Import{}, nil, domain.ImportMapping{}, err
	}
	return im, rows, m, nil
}

func mapAccount(
	ctx context.Context,
	r repository.Repos,
	userID int64,
	src domain.ImportSource,
	c domain.AccountChoice,
	now time.Time,
) error {
	var accountID int64
	switch {
	case c.AccountID != 0:
		a, err := r.Accounts.Get(ctx, userID, c.AccountID)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if err := c.CheckAccount(a); err != nil {
			return err
		}
		accountID = a.ID
	case strings.TrimSpace(c.NewName) != "":
		a, err := domain.NewAccount(domain.AccountInput{
			Name: c.NewName, Currency: c.Currency, Kind: c.NewKind, CreditLimit: c.NewCreditLimit,
		})
		if err != nil {
			return fmt.Errorf("account %q: %w", c.NewName, err)
		}
		if a, err = r.Accounts.Create(ctx, userID, a, now); err != nil {
			return err
		}
		accountID = a.ID
	default:
		return nil
	}
	return r.Imports.MapAccount(ctx, userID, src, c.Name, c.Currency, accountID)
}

func mapCategory(
	ctx context.Context,
	r repository.Repos,
	userID int64,
	src domain.ImportSource,
	c domain.CategoryChoice,
	now time.Time,
) error {
	var categoryID int64
	switch {
	case c.CategoryID != 0:
		cat, err := r.Categories.Get(ctx, userID, c.CategoryID)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if err := c.CheckCategory(cat); err != nil {
			return err
		}
		categoryID = cat.ID
	case strings.TrimSpace(c.Cat1) != "":
		cat, err := ensureCategory(ctx, r, userID, c.Kind, c.Cat1, c.Cat2, now)
		if err != nil {
			return fmt.Errorf("category %q: %w", domain.FullName(c.Cat1, c.Cat2), err)
		}
		categoryID = cat.ID
	default:
		return nil
	}
	return r.Imports.MapCategory(ctx, userID, src, c.Name, c.Kind, categoryID)
}
