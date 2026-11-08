package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/ui/pages"
	"github.com/ysomad/gigafin/internal/ui/view"
)

// renderCategories renders page, the categories or the new category page, titled title, of d.
func (u *UI) renderCategories(
	w http.ResponseWriter,
	r *http.Request,
	user domain.User,
	page func(view.Page) templ.Component,
	title string,
	status int,
	d view.Categories,
	viewErr error,
) error {
	if _, err := domain.ParseKind(string(d.Kind)); err != nil {
		d.Kind = domain.KindExpense
	}
	var err error
	if d.Categories, err = u.svc.Categories.Tree(r.Context(), user.ID); err != nil {
		return err
	}
	return u.render(w, r, status, page, view.Page{
		User: &user, Nav: "settings", Title: title, Error: viewErr, Form: "category", Data: d,
	})
}

// categoryNameError returns the message about the category name input translated to
// lang, empty if name is valid.
func (u *UI) categoryNameError(name, lang string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return u.dicts.T(lang, "required")
	case utf8.RuneCountInString(name) > domain.MaxTextLen:
		return u.dicts.T(lang, "too long")
	case strings.Contains(name, " / "):
		return u.dicts.T(lang, "must not contain %q", " / ")
	}
	return ""
}

func (u *UI) categories(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := view.Categories{Kind: domain.Kind(r.URL.Query().Get("kind"))}
	return u.renderCategories(w, r, user, pages.Categories, "Categories", http.StatusOK, d, nil)
}

func (u *UI) newCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := view.Categories{Kind: domain.Kind(r.URL.Query().Get("kind"))}
	return u.renderCategories(w, r, user, pages.CategoryNew, "New category", http.StatusOK, d, nil)
}

func (u *UI) createCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var d view.Categories
	if err := u.formDecoder.Decode(&d, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p := view.PrefsFrom(r.Context())
	if msg := u.categoryNameError(d.Name, p.Lang); msg != "" {
		return u.renderCategories(
			w, r, user, pages.CategoryNew, "New category", http.StatusUnprocessableEntity, d, view.FieldsError{"name": msg},
		)
	}
	c, err := u.svc.Categories.Create(r.Context(), user.ID, d.Parent, d.Kind, d.Name)
	if isUserError(err) {
		return u.renderCategories(w, r, user, pages.CategoryNew, "New category", http.StatusUnprocessableEntity, d, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/categories?kind="+string(c.Kind))
	return nil
}

// renderCategory renders the page of category id. A nil f shows the category's values
// in the form, otherwise f holds the values of a form post that failed with viewErr.
func (u *UI) renderCategory(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, f *view.Category, viewErr error,
) error {
	c, tree, err := u.svc.Categories.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	d := view.Category{Category: c, Name: c.Name, Parent: c.ParentID}
	if f != nil {
		d.Name, d.Parent = f.Name, f.Parent
	}
	d.Parents, d.ParentName = domain.ParentChoices(tree, c)
	return u.render(w, r, status, pages.Category, view.Page{
		User: &user, Nav: "settings", Title: "%s", TitleArgs: []any{c.Name}, Error: viewErr, Form: "category",
		Data: d,
	})
}

func (u *UI) category(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	return u.renderCategory(w, r, user, id, http.StatusOK, nil, nil)
}

func (u *UI) updateCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f view.Category
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p := view.PrefsFrom(r.Context())
	if msg := u.categoryNameError(f.Name, p.Lang); msg != "" {
		return u.renderCategory(w, r, user, id, http.StatusUnprocessableEntity, &f, view.FieldsError{"name": msg})
	}
	err = u.svc.Categories.Update(r.Context(), user.ID, id, f.Name, f.Parent)
	if isUserError(err) {
		return u.renderCategory(w, r, user, id, http.StatusUnprocessableEntity, &f, err)
	}
	if err != nil {
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
