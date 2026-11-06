package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

// categoriesData is the categories page of Kind, or the new category page with the
// values of its form.
type categoriesData struct {
	Categories []domain.Category
	Kind       domain.Kind `form:"kind"`
	Parent     int64       `form:"parent"` // 0 is a cat1
	Name       string      `form:"name"`
}

// renderCategories renders page "categories" or "category_new" of d.
func (u *UI) renderCategories(
	w http.ResponseWriter, r *http.Request, user domain.User, page string, status int, d categoriesData, viewErr error,
) error {
	if _, err := domain.ParseKind(string(d.Kind)); err != nil {
		d.Kind = domain.KindExpense
	}
	var err error
	if d.Categories, err = u.svc.Categories.Tree(r.Context(), user.ID); err != nil {
		return err
	}
	title := "Categories"
	if page == "category_new" {
		title = "New category"
	}
	return u.render(w, r, status, page, view{
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
	d := categoriesData{Kind: domain.Kind(r.URL.Query().Get("kind"))}
	return u.renderCategories(w, r, user, "categories", http.StatusOK, d, nil)
}

func (u *UI) newCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := categoriesData{Kind: domain.Kind(r.URL.Query().Get("kind"))}
	return u.renderCategories(w, r, user, "category_new", http.StatusOK, d, nil)
}

func (u *UI) createCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var d categoriesData
	if err := u.formDecoder.Decode(&d, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	if msg := u.categoryNameError(d.Name, p.lang); msg != "" {
		return u.renderCategories(w, r, user, "category_new", http.StatusUnprocessableEntity, d, fieldsError{"name": msg})
	}
	c, err := u.svc.Categories.Create(r.Context(), user.ID, d.Parent, d.Kind, d.Name)
	if isUserError(err) {
		return u.renderCategories(w, r, user, "category_new", http.StatusUnprocessableEntity, d, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/categories?kind="+string(c.Kind))
	return nil
}

// categoryData is the category page with the values of its edit form.
type categoryData struct {
	Category   domain.Category
	ParentName string
	Parents    []domain.Category
	Name       string `form:"name"`
	Parent     int64  `form:"parent"` // 0 keeps the parent
}

// renderCategory renders the page of category id. A nil f shows the category's values
// in the form, otherwise f holds the values of a form post that failed with viewErr.
func (u *UI) renderCategory(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, f *categoryData, viewErr error,
) error {
	c, tree, err := u.svc.Categories.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	d := categoryData{Category: c, Name: c.Name, Parent: c.ParentID}
	if f != nil {
		d.Name, d.Parent = f.Name, f.Parent
	}
	d.Parents, d.ParentName = domain.ParentChoices(tree, c)
	return u.render(w, r, status, "category", view{
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
	var f categoryData
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	if msg := u.categoryNameError(f.Name, p.lang); msg != "" {
		return u.renderCategory(w, r, user, id, http.StatusUnprocessableEntity, &f, fieldsError{"name": msg})
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
