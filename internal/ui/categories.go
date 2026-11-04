package ui

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

type categoriesData struct {
	Categories []domain.Category
	Kind       domain.Kind
}

func (u *UI) categories(w http.ResponseWriter, r *http.Request, user domain.User) error {
	kind, err := domain.ParseKind(r.URL.Query().Get("kind"))
	if err != nil {
		kind = domain.KindExpense
	}
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	return u.render(w, r, http.StatusOK, "categories", view{
		User: &user, Nav: "settings", Title: "Categories", Data: categoriesData{Categories: tree, Kind: kind},
	})
}

func (u *UI) createCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Parent int64  `form:"parent"` // 0 is a cat1
		Kind   string `form:"kind"`
		Name   string `form:"name"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	c, err := u.svc.Categories.Create(r.Context(), user.ID, f.Parent, domain.Kind(f.Kind), f.Name)
	if err != nil {
		return err
	}
	redirect(w, r, "/categories?kind="+string(c.Kind))
	return nil
}

type categoryData struct {
	Category   domain.Category
	ParentName string
	Parents    []domain.Category
}

func (u *UI) category(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	c, tree, err := u.svc.Categories.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	d := categoryData{Category: c}
	d.Parents, d.ParentName = domain.ParentChoices(tree, c)
	return u.render(
		w,
		r,
		http.StatusOK,
		"category",
		view{User: &user, Nav: "settings", Title: "%s", TitleArgs: []any{c.Name}, Data: d},
	)
}

func (u *UI) updateCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Parent int64  `form:"parent"` // 0 keeps the parent
		Name   string `form:"name"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Categories.Update(r.Context(), user.ID, id, f.Name, f.Parent); err != nil {
		return err
	}
	redirect(w, r, "/categories")
	return nil
}

func (u *UI) archiveCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Categories.Archive(r.Context(), user.ID, id, r.PostFormValue("archived") == "1"); err != nil {
		return err
	}
	redirect(w, r, "/categories/"+r.PathValue("id"))
	return nil
}

func (u *UI) deleteCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Categories.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/categories")
	return nil
}
