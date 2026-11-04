package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Categorizer fills in operations before they are stored: their category, payee, note
// and tags. Rules do it now; a language model may follow them later. It takes a batch,
// as an import categorizes all its rows at once, and is called outside write
// transactions, so it may be slow. It returns one input per input, in order.
type Categorizer interface {
	Categorize(ctx context.Context, userID int64, ins []domain.OperationInput) ([]domain.OperationInput, error)
}

// type check
var _ Categorizer = (*Rules)(nil)

// Rules manages categorization rules and applies them.
type Rules struct {
	store *repository.Store
	clock timex.Clock
	log   *slog.Logger
}

// Categorize applies the user's rules to ins.
func (s *Rules) Categorize(
	ctx context.Context, userID int64, ins []domain.OperationInput,
) ([]domain.OperationInput, error) {
	r := s.store.Read()
	rules, err := r.Rules.List(ctx, userID)
	if err != nil || len(rules) == 0 {
		return ins, err
	}
	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	// a stored rule that no longer validates is skipped, so it does not block creating
	// and importing operations
	valid := make([]domain.Rule, 0, len(rules))
	for _, rule := range rules {
		if _, err := domain.NewRuleSet([]domain.Rule{rule}, byID); err != nil {
			s.log.WarnContext(ctx, "invalid rule skipped", slog.Int64("user_id", userID),
				slog.Int64("rule_id", rule.ID), slog.Any("error", err))
			continue
		}
		valid = append(valid, rule)
	}
	rs, err := domain.NewRuleSet(valid, byID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.OperationInput, len(ins))
	for i, in := range ins {
		out[i], _ = rs.Apply(in)
	}
	return out, nil
}

// List returns the user's rules, oldest first.
func (s *Rules) List(ctx context.Context, userID int64) ([]domain.Rule, error) {
	return s.store.Read().Rules.List(ctx, userID)
}

// Get returns one rule.
func (s *Rules) Get(ctx context.Context, userID, id int64) (domain.Rule, error) {
	return s.store.Read().Rules.Get(ctx, userID, id)
}

// Create validates and stores a rule.
func (s *Rules) Create(ctx context.Context, userID int64, in domain.RuleInput) (domain.Rule, error) {
	var rule domain.Rule
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		cats, err := r.Categories.List(ctx, userID)
		if err != nil {
			return err
		}
		byID := make(map[int64]domain.Category, len(cats))
		for _, c := range cats {
			byID[c.ID] = c
		}
		if rule, err = domain.NewRule(in, domain.RuleOriginUser, byID); err != nil {
			return err
		}
		rule, err = r.Rules.Create(ctx, userID, rule, s.clock.Now())
		return err
	})
	if err != nil {
		return rule, err
	}
	s.log.DebugContext(ctx, "rule created", slog.Int64("user_id", userID), slog.Int64("rule_id", rule.ID))
	return rule, nil
}

// Update replaces a rule. An edited rule is the user's, even when it was learned.
func (s *Rules) Update(ctx context.Context, userID, id int64, in domain.RuleInput) (domain.Rule, error) {
	var rule domain.Rule
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		cats, err := r.Categories.List(ctx, userID)
		if err != nil {
			return err
		}
		byID := make(map[int64]domain.Category, len(cats))
		for _, c := range cats {
			byID[c.ID] = c
		}
		if rule, err = domain.NewRule(in, domain.RuleOriginUser, byID); err != nil {
			return err
		}
		rule.ID = id
		rule, err = r.Rules.Update(ctx, userID, rule, s.clock.Now())
		return err
	})
	if err != nil {
		return rule, err
	}
	s.log.DebugContext(ctx, "rule updated", slog.Int64("user_id", userID), slog.Int64("rule_id", id))
	return rule, nil
}

// Delete deletes a rule.
func (s *Rules) Delete(ctx context.Context, userID, id int64) error {
	if err := s.store.Write().Rules.Delete(ctx, userID, id); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "rule deleted", slog.Int64("user_id", userID), slog.Int64("rule_id", id))
	return nil
}

// Settings returns the user's rule settings.
func (s *Rules) Settings(ctx context.Context, userID int64) (domain.RuleSettings, error) {
	return s.store.Read().Rules.Settings(ctx, userID)
}

// SetLearning turns learning rules from the user's categorization on or off.
func (s *Rules) SetLearning(ctx context.Context, userID int64, on bool) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		rs, err := r.Rules.Settings(ctx, userID)
		if err != nil {
			return err
		}
		rs.Learning = on
		return r.Rules.SetSettings(ctx, userID, rs, s.clock.Now())
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "rule learning set", slog.Int64("user_id", userID), slog.Bool("learning", on))
	return nil
}

// Preview validates in and returns up to limit of the user's operations it matches,
// newest first, and how many it matches in all. Parts of splits are left out, as Apply
// leaves them alone.
func (s *Rules) Preview(
	ctx context.Context, userID int64, in domain.RuleInput, limit int,
) ([]domain.Operation, int, error) {
	r := s.store.Read()
	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[int64]domain.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	rule, err := domain.NewRule(in, domain.RuleOriginUser, byID)
	if err != nil {
		return nil, 0, err
	}
	rs, err := domain.NewRuleSet([]domain.Rule{rule}, byID)
	if err != nil {
		return nil, 0, err
	}
	ops, err := allOperations(ctx, r, userID, domain.Filter{})
	if err != nil {
		return nil, 0, err
	}
	var (
		matched []domain.Operation
		total   int
	)
	for _, op := range ops {
		if op.IsSplit() || !rs.Matches(rule.ID, op.Input()) {
			continue
		}
		total++
		if len(matched) < limit {
			matched = append(matched, op)
		}
	}
	return matched, total, nil
}

// Apply applies the rule with id to all the user's operations it matches, except parts
// of splits, and returns how many operations changed.
func (s *Rules) Apply(ctx context.Context, userID, id int64) (int, error) {
	r := s.store.Read()
	rule, err := r.Rules.Get(ctx, userID, id)
	if err != nil {
		return 0, err
	}
	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return 0, err
	}
	byID := make(map[int64]domain.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	rs, err := domain.NewRuleSet([]domain.Rule{rule}, byID)
	if err != nil {
		return 0, err
	}
	ops, err := allOperations(ctx, r, userID, domain.Filter{})
	if err != nil {
		return 0, err
	}
	var changed []domain.Operation
	ins := make(map[int64]domain.OperationInput)
	for _, op := range ops {
		if op.IsSplit() {
			continue
		}
		in, ok := rs.Apply(op.Input())
		if !ok {
			continue
		}
		changed = append(changed, op)
		ins[op.ID] = in
	}
	n, err := replaceOperations(ctx, s.store, userID, changed, ins, s.clock.Now())
	if err != nil {
		return n, err
	}
	s.log.InfoContext(ctx, "rule applied", slog.Int64("user_id", userID), slog.Int64("rule_id", id),
		slog.Int("count", n))
	return n, nil
}

// learnCategory learns a rule from the user setting the category of op by hand, see
// domain.LearnCategory, when learning is on. It returns how many rules it stored.
func learnCategory(
	ctx context.Context, r repository.Repos, userID int64, op domain.Operation, now time.Time,
) (int, error) {
	const window = 180 // days around the operation, as Actual Budget looks
	if op.Payee == "" || op.CategoryID == 0 {
		return 0, nil
	}
	settings, err := r.Rules.Settings(ctx, userID)
	if err != nil || !settings.Learning {
		return 0, err
	}
	history, err := r.Operations.ByPayee(
		ctx, userID, op.Payee, op.Date.AddDate(0, 0, -window), op.Date.AddDate(0, 0, window),
	)
	if err != nil {
		return 0, err
	}
	rules, err := r.Rules.List(ctx, userID)
	if err != nil {
		return 0, err
	}
	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return 0, err
	}
	byID := make(map[int64]domain.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	learned, ok := domain.LearnCategory(history, op, rules, byID)
	if !ok {
		return 0, nil
	}
	for _, rule := range learned {
		if rule.ID == 0 {
			_, err = r.Rules.Create(ctx, userID, rule, now)
		} else {
			_, err = r.Rules.Update(ctx, userID, rule, now)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(learned), nil
}

// allOperations returns all the user's operations matching f, newest first, reading
// them page by page.
func allOperations(ctx context.Context, r repository.Repos, userID int64, f domain.Filter) ([]domain.Operation, error) {
	const page = 500
	var (
		all    []domain.Operation
		cursor pagination.Cursor
	)
	for {
		ops, err := r.Operations.List(ctx, userID, f, cursor, page)
		if err != nil {
			return nil, err
		}
		all = append(all, ops...)
		if len(ops) < page {
			return all, nil
		}
		last := ops[len(ops)-1]
		cursor = pagination.Cursor{Date: last.Date, ID: last.ID}
	}
}

// replaceOperations replaces each operation of ops with its input in ins in one
// transaction, validated as an update would be, and returns how many it replaced.
func replaceOperations(
	ctx context.Context, store *repository.Store, userID int64, ops []domain.Operation,
	ins map[int64]domain.OperationInput, now time.Time,
) (int, error) {
	if len(ops) == 0 {
		return 0, nil
	}
	n := 0
	err := store.InTx(ctx, func(r repository.Repos) error {
		for _, old := range ops {
			op, err := newOperation(ctx, r, userID, ins[old.ID])
			if err != nil {
				return domain.NewError(err, "operation %d", old.ID)
			}
			op.ID, op.OriginID = old.ID, old.OriginID
			if _, err := r.Operations.Update(ctx, userID, op, now); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
