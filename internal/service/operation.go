package service

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Operations manages expenses, income, transfers and adjustments.
type Operations struct {
	store       *repository.Store
	clock       timex.Clock
	log         *slog.Logger
	categorizer Categorizer
}

// Create validates and stores an operation. Rules fill the payee, note and category
// when they are empty, so an expense or income may come without a category. A category
// the user gave may teach a rule, see domain.LearnCategory.
func (s *Operations) Create(ctx context.Context, userID int64, in domain.OperationInput) (domain.Operation, error) {
	filled, err := s.Fill(ctx, userID, in)
	if err != nil {
		return domain.Operation{}, err
	}
	var (
		op      domain.Operation
		learned int
	)
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		now := s.clock.Now()
		var err error
		if op, err = createOperation(ctx, r, userID, filled, domain.ImportRef{}, now); err != nil {
			return err
		}
		if in.CategoryID == 0 {
			return nil
		}
		learned, err = learnCategory(ctx, r, userID, op, now)
		return err
	})
	if err != nil {
		return op, err
	}
	s.log.DebugContext(ctx, "operation created", slog.Int64("user_id", userID), slog.Int64("operation_id", op.ID))
	if learned > 0 {
		s.log.DebugContext(ctx, "rules learned", slog.Int64("user_id", userID), slog.Int64("operation_id", op.ID),
			slog.Int("count", learned))
	}
	return op, nil
}

// Fill returns in with the empty payee, note and category filled by rules, as Create
// stores it, so a form can show them before saving.
func (s *Operations) Fill(ctx context.Context, userID int64, in domain.OperationInput) (domain.OperationInput, error) {
	ruled, err := s.categorizer.Categorize(ctx, userID, []domain.OperationInput{in})
	if err != nil {
		return domain.OperationInput{}, err
	}
	return domain.FillEmpty(in, ruled[0]), nil
}

// Categorize runs rules on the user's operations matching f, except parts of splits,
// and returns how many operations changed.
func (s *Operations) Categorize(ctx context.Context, userID int64, f domain.Filter) (int, error) {
	if err := f.Validate(); err != nil {
		return 0, err
	}
	all, err := allOperations(ctx, s.store.Read(), userID, f)
	if err != nil {
		return 0, err
	}
	var ops []domain.Operation
	for _, op := range all {
		if !op.IsSplit() {
			ops = append(ops, op)
		}
	}
	ins := make([]domain.OperationInput, len(ops))
	for i, op := range ops {
		ins[i] = op.Input()
	}
	ruled, err := s.categorizer.Categorize(ctx, userID, ins)
	if err != nil {
		return 0, err
	}
	var changed []domain.Operation
	byID := make(map[int64]domain.OperationInput)
	for i, op := range ops {
		in, out := ins[i], ruled[i]
		if in.Payee == out.Payee && in.Note == out.Note && in.CategoryID == out.CategoryID &&
			slices.Equal(in.Tags, out.Tags) {
			continue
		}
		changed = append(changed, op)
		byID[op.ID] = out
	}
	n, err := replaceOperations(ctx, s.store, userID, changed, byID, s.clock.Now())
	if err != nil {
		return n, err
	}
	s.log.InfoContext(ctx, "operations categorized", slog.Int64("user_id", userID), slog.Int("count", n))
	return n, nil
}

// Update replaces an operation. A part of a split stays in its split. Rules do not
// run, but a changed category or payee may teach a rule, see domain.LearnCategory.
func (s *Operations) Update(
	ctx context.Context, userID, id int64, in domain.OperationInput,
) (domain.Operation, error) {
	var (
		op      domain.Operation
		learned int
	)
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		old, err := r.Operations.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		if op, err = newOperation(ctx, r, userID, in); err != nil {
			return err
		}
		now := s.clock.Now()
		op.ID, op.OriginID = id, old.OriginID
		if op, err = r.Operations.Update(ctx, userID, op, now); err != nil {
			return err
		}
		if op.CategoryID == old.CategoryID && op.Payee == old.Payee {
			return nil
		}
		learned, err = learnCategory(ctx, r, userID, op, now)
		return err
	})
	if err != nil {
		return op, err
	}
	s.log.DebugContext(ctx, "operation updated", slog.Int64("user_id", userID), slog.Int64("operation_id", id))
	if learned > 0 {
		s.log.DebugContext(ctx, "rules learned", slog.Int64("user_id", userID), slog.Int64("operation_id", id),
			slog.Int("count", learned))
	}
	return op, nil
}

// Split splits an expense or income into parts that add up to its amount and returns
// them, the operation itself first. Each part is an operation of its own afterwards.
func (s *Operations) Split(
	ctx context.Context, userID, id int64, parts []domain.SplitPart,
) ([]domain.Operation, error) {
	var ops []domain.Operation
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		op, err := r.Operations.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		ops, err = splitOperation(ctx, r, userID, op, parts, s.clock.Now())
		return err
	})
	if err != nil {
		return ops, err
	}
	s.log.DebugContext(ctx, "operation split", slog.Int64("user_id", userID), slog.Int64("operation_id", id),
		slog.Int("parts", len(ops)))
	return ops, nil
}

// Delete deletes an operation. Other parts of its split are kept.
func (s *Operations) Delete(ctx context.Context, userID, id int64) error {
	if err := s.store.Write().Operations.Delete(ctx, userID, id); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "operation deleted", slog.Int64("user_id", userID), slog.Int64("operation_id", id))
	return nil
}

// Get returns one operation.
func (s *Operations) Get(ctx context.Context, userID, id int64) (domain.Operation, error) {
	return s.store.Read().Operations.Get(ctx, userID, id)
}

// Totals returns income and expense of all operations matching f per currency.
func (s *Operations) Totals(ctx context.Context, userID int64, f domain.Filter) ([]domain.CashFlow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	sums, err := s.store.Read().Operations.Sum(ctx, userID, f)
	if err != nil {
		return nil, err
	}
	return domain.CashFlows(sums), nil
}

// Tags returns the names of the user's tags, sorted.
func (s *Operations) Tags(ctx context.Context, userID int64) ([]string, error) {
	return s.store.Read().Operations.Tags(ctx, userID)
}

// Months returns the months from the user's first operation to the last one, latest
// first, widened to cover the months of include.
func (s *Operations) Months(ctx context.Context, userID int64, include ...timex.Month) ([]timex.Month, error) {
	first, last, err := s.store.Read().Operations.DateRange(ctx, userID)
	if err != nil {
		return nil, err
	}
	var from, to timex.Month
	if !first.IsZero() {
		from, to = timex.MonthOf(first), timex.MonthOf(last)
	}
	for _, m := range include {
		if from.IsZero() || m.First().Before(from.First()) {
			from = m
		}
		if to.IsZero() || m.First().After(to.First()) {
			to = m
		}
	}
	if from.IsZero() {
		return nil, nil
	}
	var months []timex.Month
	for m := to; !m.First().Before(from.First()); m = m.Prev() {
		months = append(months, m)
	}
	return months, nil
}

// List returns a page of operations matching f, newest first, and the cursor of the
// next page, which is zero on the last page.
func (s *Operations) List(
	ctx context.Context, userID int64, f domain.Filter, p pagination.Params,
) ([]domain.Operation, pagination.Cursor, error) {
	if err := f.Validate(); err != nil {
		return nil, pagination.Cursor{}, err
	}
	limit := p.PageLimit()
	ops, err := s.store.Read().Operations.List(ctx, userID, f, p.After, limit+1)
	if err != nil {
		return nil, pagination.Cursor{}, err
	}
	ops, next := pagination.Cut(ops, limit, func(op domain.Operation) pagination.Cursor {
		return pagination.Cursor{Date: op.Date, ID: op.ID}
	})
	return ops, next, nil
}

func createOperation(
	ctx context.Context, r repository.Repos, userID int64, in domain.OperationInput, ref domain.ImportRef,
	now time.Time,
) (domain.Operation, error) {
	op, err := newOperation(ctx, r, userID, in)
	if err != nil {
		return domain.Operation{}, err
	}
	return r.Operations.Create(ctx, userID, op, ref, now)
}

// splitOperation splits op into parts in the caller's transaction. New parts belong to
// the import of op, but not to its import row.
func splitOperation(
	ctx context.Context, r repository.Repos, userID int64, op domain.Operation, parts []domain.SplitPart,
	now time.Time,
) ([]domain.Operation, error) {
	ids := make([]int64, len(parts))
	for i, p := range parts {
		ids[i] = p.CategoryID
	}
	cats, err := r.Categories.ByIDs(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	split, err := domain.SplitOperation(op, parts, cats)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Operation, len(split))
	for i, part := range split {
		if i == 0 {
			part, err = r.Operations.Update(ctx, userID, part, now)
		} else {
			part, err = r.Operations.Create(ctx, userID, part, domain.ImportRef{ImportID: op.ImportID}, now)
		}
		if err != nil {
			return nil, err
		}
		out[i] = part
	}
	return out, nil
}

// newOperation loads the accounts and category in refers to and validates it.
func newOperation(
	ctx context.Context, r repository.Repos, userID int64, in domain.OperationInput,
) (domain.Operation, error) {
	acc, err := r.Accounts.Get(ctx, userID, in.AccountID)
	if err != nil {
		return domain.Operation{}, err
	}
	var to *domain.Account
	if in.Type == domain.OpTransfer && in.ToAccountID != 0 && in.ToAccountID != in.AccountID {
		a, err := r.Accounts.Get(ctx, userID, in.ToAccountID)
		switch {
		case err == nil:
			to = &a
		case !errors.Is(err, errors.ErrNotFound):
			return domain.Operation{}, err
		}
	}
	var cat *domain.Category
	if in.CategoryID != 0 {
		c, err := r.Categories.Get(ctx, userID, in.CategoryID)
		switch {
		case err == nil:
			cat = &c
		case !errors.Is(err, errors.ErrNotFound):
			return domain.Operation{}, err
		}
	}
	var known []string
	if len(in.Tags) > 0 {
		if known, err = r.Operations.Tags(ctx, userID); err != nil {
			return domain.Operation{}, err
		}
	}
	return domain.NewOperation(in, acc, to, cat, known)
}
