package domain

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
)

// Kind is the kind of a category: expense or income.
type Kind string

const (
	KindExpense Kind = "expense"
	KindIncome  Kind = "income"
)

// _nameSep joins cat1 and cat2 in display names, so names must not contain it.
const _nameSep = " / "

// Kinds returns all category kinds.
func Kinds() []Kind {
	return []Kind{KindExpense, KindIncome}
}

// ParseKind validates a kind string.
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if !slices.Contains(Kinds(), k) {
		return "", NewError(errors.ErrInvalid, "kind: must be expense or income")
	}
	return k, nil
}

// Category is a cat1 (ParentID == 0) or a cat2 under a cat1. Operations may
// use either level.
type Category struct {
	ID       int64
	ParentID int64
	Kind     Kind
	Name     string
	Archived bool
	// Children is filled for cat1 by NewTree.
	Children []Category
}

// FullName returns "cat1 / cat2" for a cat2 and the name for a cat1.
func FullName(parent, child string) string {
	if child == "" {
		return parent
	}
	return parent + _nameSep + child
}

// NewCategory validates a new cat1 of kind, or a cat2 under parent, which takes the
// parent kind.
func NewCategory(parent *Category, kind Kind, name string) (Category, error) {
	name, err := categoryName(name)
	if err != nil {
		return Category{}, err
	}
	c := Category{Name: name, Kind: kind}
	if parent != nil {
		if parent.ParentID != 0 {
			return Category{}, NewError(errors.ErrInvalid, "parent: categories can be nested only one level deep")
		}
		c.ParentID, c.Kind = parent.ID, parent.Kind
	}
	if _, err := ParseKind(string(c.Kind)); err != nil {
		return Category{}, err
	}
	return c, nil
}

// Edit renames the category and, for a cat2, moves it under parent when it is not nil.
func (c Category) Edit(name string, parent *Category) (Category, error) {
	name, err := categoryName(name)
	if err != nil {
		return Category{}, err
	}
	c.Name = name
	if parent == nil {
		return c, nil
	}
	if c.ParentID == 0 {
		return Category{}, NewError(
			errors.ErrInvalid,
			"only a category inside another category can be moved",
		)
	}
	if parent.ParentID != 0 || parent.Kind != c.Kind {
		return Category{}, NewError(
			errors.ErrInvalid,
			"parent must be an %s category that is not inside another category",
			c.Kind,
		)
	}
	c.ParentID = parent.ID
	return c, nil
}

// CheckDelete fails with errors.ErrConflict when the category has children or is used
// by operations or rules.
func (c Category) CheckDelete(children, ops, rules int64) error {
	if children > 0 {
		return NewError(errors.ErrConflict, "category contains %d other categories", children)
	}
	if ops > 0 {
		return NewError(errors.ErrConflict, "category is used by %d operations, archive it instead", ops)
	}
	if rules > 0 {
		return NewError(errors.ErrConflict, "category is used by %d rules", rules)
	}
	return nil
}

// NewTree turns a flat list into cat1 with their cat2 children, keeping the order.
func NewTree(cats []Category) []Category {
	children := make(map[int64][]Category)
	for _, c := range cats {
		if c.ParentID != 0 {
			children[c.ParentID] = append(children[c.ParentID], c)
		}
	}
	var roots []Category
	for _, c := range cats {
		if c.ParentID == 0 {
			c.Children = children[c.ID]
			roots = append(roots, c)
		}
	}
	return roots
}

// ByKind returns the cat1 of kind.
func ByKind(tree []Category, kind Kind) []Category {
	out := make([]Category, 0, len(tree))
	for _, c := range tree {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// CategoryNames maps every category id to its display name ("cat1 / cat2" for cat2).
func CategoryNames(tree []Category) map[int64]string {
	names := make(map[int64]string, len(tree))
	for _, c1 := range tree {
		names[c1.ID] = c1.Name
		for _, c2 := range c1.Children {
			names[c2.ID] = FullName(c1.Name, c2.Name)
		}
	}
	return names
}

// ParentChoices returns the cat1 a cat2 c can move under, without their children, and
// the name of its current parent. Both are empty for a cat1.
func ParentChoices(tree []Category, c Category) ([]Category, string) {
	if c.ParentID == 0 {
		return nil, ""
	}
	cats := ByKind(tree, c.Kind)
	parents := make([]Category, len(cats))
	var name string
	for i, p := range cats {
		if p.ID == c.ParentID {
			name = p.Name
		}
		p.Children = nil
		parents[i] = p
	}
	return parents, name
}

func categoryName(s string) (string, error) {
	name := strings.TrimSpace(s)
	if name == "" {
		return "", NewError(errors.ErrInvalid, "name: required")
	}
	if utf8.RuneCountInString(name) > MaxTextLen {
		return "", NewError(errors.ErrInvalid, "name: too long")
	}
	if strings.Contains(name, _nameSep) {
		return "", NewError(errors.ErrInvalid, "name: must not contain %q", _nameSep)
	}
	return name, nil
}
