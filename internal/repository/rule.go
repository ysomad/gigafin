package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
)

// Rules stores categorization rules and their settings.
type Rules struct {
	q *sqlc.Queries
}

// List returns the user's rules, oldest first.
func (r *Rules) List(ctx context.Context, userID int64) ([]domain.Rule, error) {
	rows, err := r.q.ListRules(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	rules := make([]domain.Rule, len(rows))
	for i, row := range rows {
		if rules[i], err = ruleFromRow(row); err != nil {
			return nil, err
		}
	}
	return rules, nil
}

// Get returns one rule.
func (r *Rules) Get(ctx context.Context, userID, id int64) (domain.Rule, error) {
	row, err := r.q.GetRule(ctx, sqlc.GetRuleParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Rule{}, notFound(err, "rule")
	}
	return ruleFromRow(row)
}

// Create stores a validated rule and returns it as stored.
func (r *Rules) Create(ctx context.Context, userID int64, rule domain.Rule, now time.Time) (domain.Rule, error) {
	conds, err := json.Marshal(rule.Conditions)
	if err != nil {
		return domain.Rule{}, fmt.Errorf("marshal rule conditions: %w", err)
	}
	actions, err := json.Marshal(rule.Actions)
	if err != nil {
		return domain.Rule{}, fmt.Errorf("marshal rule actions: %w", err)
	}
	row, err := r.q.CreateRule(ctx, sqlc.CreateRuleParams{
		UserID:     userID,
		Stage:      string(rule.Stage),
		Match:      string(rule.Match),
		Conditions: string(conds),
		Actions:    string(actions),
		Origin:     string(rule.Origin),
		CreatedAt:  now.Unix(),
	})
	if err != nil {
		return domain.Rule{}, fmt.Errorf("create rule: %w", err)
	}
	return ruleFromRow(row)
}

// Update replaces the rule with id rule.ID and returns it as stored.
func (r *Rules) Update(ctx context.Context, userID int64, rule domain.Rule, now time.Time) (domain.Rule, error) {
	conds, err := json.Marshal(rule.Conditions)
	if err != nil {
		return domain.Rule{}, fmt.Errorf("marshal rule conditions: %w", err)
	}
	actions, err := json.Marshal(rule.Actions)
	if err != nil {
		return domain.Rule{}, fmt.Errorf("marshal rule actions: %w", err)
	}
	row, err := r.q.UpdateRule(ctx, sqlc.UpdateRuleParams{
		Stage:      string(rule.Stage),
		Match:      string(rule.Match),
		Conditions: string(conds),
		Actions:    string(actions),
		Origin:     string(rule.Origin),
		UpdatedAt:  sql.NullInt64{Int64: now.Unix(), Valid: true},
		ID:         rule.ID,
		UserID:     userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Rule{}, fmt.Errorf("update rule: %w", errors.ErrNotFound)
	}
	if err != nil {
		return domain.Rule{}, fmt.Errorf("update rule: %w", err)
	}
	return ruleFromRow(row)
}

// Delete deletes one rule.
func (r *Rules) Delete(ctx context.Context, userID, id int64) error {
	n, err := r.q.DeleteRule(ctx, sqlc.DeleteRuleParams{ID: id, UserID: userID})
	return affected(n, err, "delete rule")
}

// DeleteAll deletes all rules of the user.
func (r *Rules) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserRules(ctx, userID); err != nil {
		return fmt.Errorf("delete rules: %w", err)
	}
	return nil
}

func ruleFromRow(row sqlc.Rule) (domain.Rule, error) {
	rule := domain.Rule{
		ID:        row.ID,
		Stage:     domain.RuleStage(row.Stage),
		Match:     domain.RuleMatch(row.Match),
		Origin:    domain.RuleOrigin(row.Origin),
		CreatedAt: time.Unix(row.CreatedAt, 0),
	}
	if err := json.Unmarshal([]byte(row.Conditions), &rule.Conditions); err != nil {
		return domain.Rule{}, fmt.Errorf("rule %d conditions: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.Actions), &rule.Actions); err != nil {
		return domain.Rule{}, fmt.Errorf("rule %d actions: %w", row.ID, err)
	}
	if row.UpdatedAt.Valid {
		rule.UpdatedAt = time.Unix(row.UpdatedAt.Int64, 0)
	}
	return rule, nil
}
