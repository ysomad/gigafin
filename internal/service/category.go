package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Categories manages the two-level category tree.
type Categories struct {
	store *repository.Store
	clock timex.Clock
	log   *slog.Logger
}

// Tree returns the cat1 tree with cat2 children, expense kinds first.
func (s *Categories) Tree(ctx context.Context, userID int64) ([]domain.Category, error) {
	cats, err := s.store.Read().Categories.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	return domain.NewTree(cats), nil
}

// Get returns one category and the tree it is in, read in one query.
func (s *Categories) Get(ctx context.Context, userID, id int64) (domain.Category, []domain.Category, error) {
	cats, err := s.store.Read().Categories.List(ctx, userID)
	if err != nil {
		return domain.Category{}, nil, err
	}
	for _, c := range cats {
		if c.ID == id {
			return c, domain.NewTree(cats), nil
		}
	}
	return domain.Category{}, nil, fmt.Errorf("category: %w", errors.ErrNotFound)
}

// Create creates a cat1 (parentID == 0, kind required) or a cat2 under parentID,
// which takes the parent kind.
func (s *Categories) Create(
	ctx context.Context, userID, parentID int64, kind domain.Kind, name string,
) (domain.Category, error) {
	var c domain.Category
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		var err error
		c, err = createCategory(ctx, r, userID, parentID, kind, name, s.clock.Now())
		return err
	})
	if err != nil {
		return c, err
	}
	s.log.DebugContext(ctx, "category created", slog.Int64("user_id", userID), slog.Int64("category_id", c.ID))
	return c, nil
}

// Update renames a category and, for a cat2, moves it under another cat1 of the same
// kind (parentID == 0 keeps the current parent).
func (s *Categories) Update(ctx context.Context, userID, id int64, name string, parentID int64) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		c, err := r.Categories.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		var parent *domain.Category
		if parentID != 0 {
			p, err := r.Categories.Get(ctx, userID, parentID)
			if err != nil {
				return fmt.Errorf("parent: %w", err)
			}
			parent = &p
		}
		if c, err = c.Edit(name, parent); err != nil {
			return err
		}
		return r.Categories.Update(ctx, userID, c)
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "category updated", slog.Int64("user_id", userID), slog.Int64("category_id", id))
	return nil
}

// Archive hides or shows a category in pickers.
func (s *Categories) Archive(ctx context.Context, userID, id int64, archived bool) error {
	var at time.Time
	if archived {
		at = s.clock.Now()
	}
	if err := s.store.Write().Categories.SetArchived(ctx, userID, id, at); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "category archived", slog.Int64("user_id", userID), slog.Int64("category_id", id),
		slog.Bool("archived", archived))
	return nil
}

// Delete deletes a category with no children, operations or rules.
func (s *Categories) Delete(ctx context.Context, userID, id int64) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		c, err := r.Categories.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		children, err := r.Categories.CountChildren(ctx, id)
		if err != nil {
			return err
		}
		ops, err := r.Categories.CountOperations(ctx, id)
		if err != nil {
			return err
		}
		rules, err := r.Rules.List(ctx, userID)
		if err != nil {
			return err
		}
		var used int64
		for _, rule := range rules {
			if rule.UsesCategory(id) {
				used++
			}
		}
		if err := c.CheckDelete(children, ops, used); err != nil {
			return err
		}
		return r.Categories.Delete(ctx, userID, id)
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "category deleted", slog.Int64("user_id", userID), slog.Int64("category_id", id))
	return nil
}

func createCategory(
	ctx context.Context, r repository.Repos, userID, parentID int64, kind domain.Kind, name string, now time.Time,
) (domain.Category, error) {
	var parent *domain.Category
	if parentID != 0 {
		p, err := r.Categories.Get(ctx, userID, parentID)
		if err != nil {
			return domain.Category{}, fmt.Errorf("parent: %w", err)
		}
		parent = &p
	}
	c, err := domain.NewCategory(parent, kind, name)
	if err != nil {
		return domain.Category{}, err
	}
	return r.Categories.Create(ctx, userID, c, now)
}

// ensureCategory returns the category "cat1 / cat2" of kind, or just cat1 when cat2
// is empty, creating missing levels.
func ensureCategory(
	ctx context.Context, r repository.Repos, userID int64, kind domain.Kind, cat1, cat2 string, now time.Time,
) (domain.Category, error) {
	cat1, cat2 = strings.TrimSpace(cat1), strings.TrimSpace(cat2)
	root, err := r.Categories.RootByName(ctx, userID, kind, cat1)
	if errors.Is(err, errors.ErrNotFound) {
		root, err = createCategory(ctx, r, userID, 0, kind, cat1, now)
	}
	if err != nil || cat2 == "" {
		return root, err
	}
	child, err := r.Categories.ChildByName(ctx, root.ID, cat2)
	if errors.Is(err, errors.ErrNotFound) {
		return createCategory(ctx, r, userID, root.ID, kind, cat2, now)
	}
	return child, err
}
