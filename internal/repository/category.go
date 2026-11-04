package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
)

// Categories stores the two-level category tree.
type Categories struct {
	q *sqlc.Queries
}

// List returns all categories of the user, flat, ordered by kind and name.
func (r *Categories) List(ctx context.Context, userID int64) ([]domain.Category, error) {
	rows, err := r.q.ListCategories(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	out := make([]domain.Category, len(rows))
	for i, c := range rows {
		out[i] = categoryFromRow(c)
	}
	return out, nil
}

// ByIDs returns the user's categories among ids, keyed by id. Missing ids are absent.
func (r *Categories) ByIDs(ctx context.Context, userID int64, ids []int64) (map[int64]domain.Category, error) {
	out := make(map[int64]domain.Category, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.ListCategoriesByID(ctx, sqlc.ListCategoriesByIDParams{UserID: userID, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	for _, c := range rows {
		out[c.ID] = categoryFromRow(c)
	}
	return out, nil
}

// Get returns one category.
func (r *Categories) Get(ctx context.Context, userID, id int64) (domain.Category, error) {
	c, err := r.q.GetCategory(ctx, sqlc.GetCategoryParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Category{}, notFound(err, "category")
	}
	return categoryFromRow(c), nil
}

// RootByName returns the cat1 of kind named name.
func (r *Categories) RootByName(
	ctx context.Context, userID int64, kind domain.Kind, name string,
) (domain.Category, error) {
	c, err := r.q.GetRootCategoryByName(ctx, sqlc.GetRootCategoryByNameParams{
		UserID: userID, Kind: string(kind), Name: name,
	})
	if err != nil {
		return domain.Category{}, notFound(err, "category")
	}
	return categoryFromRow(c), nil
}

// ChildByName returns the cat2 named name under parentID.
func (r *Categories) ChildByName(ctx context.Context, parentID int64, name string) (domain.Category, error) {
	c, err := r.q.GetChildCategoryByName(ctx, sqlc.GetChildCategoryByNameParams{
		ParentID: sql.NullInt64{Int64: parentID, Valid: parentID != 0}, Name: name,
	})
	if err != nil {
		return domain.Category{}, notFound(err, "category")
	}
	return categoryFromRow(c), nil
}

// Create stores a new category and returns it with its id.
func (r *Categories) Create(
	ctx context.Context, userID int64, c domain.Category, now time.Time,
) (domain.Category, error) {
	row, err := r.q.CreateCategory(ctx, sqlc.CreateCategoryParams{
		UserID:    userID,
		ParentID:  sql.NullInt64{Int64: c.ParentID, Valid: c.ParentID != 0},
		Kind:      string(c.Kind),
		Name:      c.Name,
		CreatedAt: now.Unix(),
	})
	if k, ok := constraint(err); ok {
		return domain.Category{}, fmt.Errorf("category %q: %w", c.Name, k)
	}
	if err != nil {
		return domain.Category{}, fmt.Errorf("create category: %w", err)
	}
	return categoryFromRow(row), nil
}

// Update stores the name and parent of c.
func (r *Categories) Update(ctx context.Context, userID int64, c domain.Category) error {
	n, err := r.q.UpdateCategory(ctx, sqlc.UpdateCategoryParams{
		Name: c.Name, ParentID: sql.NullInt64{Int64: c.ParentID, Valid: c.ParentID != 0}, ID: c.ID, UserID: userID,
	})
	if k, ok := constraint(err); ok {
		return fmt.Errorf("category %q: %w", c.Name, k)
	}
	return affected(n, err, "update category")
}

// SetArchived archives the category at the given time, or unarchives it for the zero
// time.
func (r *Categories) SetArchived(ctx context.Context, userID, id int64, at time.Time) error {
	n, err := r.q.SetCategoryArchived(ctx, sqlc.SetCategoryArchivedParams{
		ArchivedAt: nullUnix(at), ID: id, UserID: userID,
	})
	return affected(n, err, "archive category")
}

// CountChildren counts the cat2 of a cat1.
func (r *Categories) CountChildren(ctx context.Context, id int64) (int64, error) {
	n, err := r.q.CountCategoryChildren(ctx, sql.NullInt64{Int64: id, Valid: id != 0})
	if err != nil {
		return 0, fmt.Errorf("count children: %w", err)
	}
	return n, nil
}

// CountOperations counts operations in the category.
func (r *Categories) CountOperations(ctx context.Context, id int64) (int64, error) {
	n, err := r.q.CountCategoryOperations(ctx, sql.NullInt64{Int64: id, Valid: id != 0})
	if err != nil {
		return 0, fmt.Errorf("count operations: %w", err)
	}
	return n, nil
}

// Delete deletes a category.
func (r *Categories) Delete(ctx context.Context, userID, id int64) error {
	n, err := r.q.DeleteCategory(ctx, sqlc.DeleteCategoryParams{ID: id, UserID: userID})
	return affected(n, err, "delete category")
}

// DeleteAll deletes every category of the user, cat2 before cat1. Operations,
// plans and import mappings must be deleted first.
func (r *Categories) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserSubcategories(ctx, userID); err != nil {
		return fmt.Errorf("delete subcategories: %w", err)
	}
	if err := r.q.DeleteUserCategories(ctx, userID); err != nil {
		return fmt.Errorf("delete categories: %w", err)
	}
	return nil
}

func categoryFromRow(c sqlc.Category) domain.Category {
	return domain.Category{
		ID:       c.ID,
		ParentID: c.ParentID.Int64,
		Kind:     domain.Kind(c.Kind),
		Name:     c.Name,
		Archived: c.ArchivedAt.Valid,
	}
}
