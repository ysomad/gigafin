// Package categories serves the categories pages.
package categories

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the categories.
type Handler struct {
	app *kit.App
}

// New returns the handler of app.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Routes returns the routes of the categories.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /categories":               h.categories,
		"GET /categories/new":           h.newCategory,
		"POST /categories":              h.createCategory,
		"GET /categories/{id}":          h.category,
		"POST /categories/{id}":         h.updateCategory,
		"POST /categories/{id}/archive": h.archiveCategory,
		"POST /categories/{id}/delete":  h.deleteCategory,
	}
}

// renderCategories renders page, the categories or the new category page, titled title, of d.
func (h *Handler) renderCategories(
	w http.ResponseWriter,
	r *http.Request,
	user domain.User,
	page func(kit.Page, categoriesData) templ.Component,
	title string,
	status int,
	d categoriesData,
	viewErr error,
) error {
	if _, err := domain.ParseKind(string(d.Kind)); err != nil {
		d.Kind = domain.KindExpense
	}
	var err error
	if d.Categories, err = h.app.Svc.Categories.Tree(r.Context(), user.ID); err != nil {
		return err
	}
	return kit.Render(h.app, w, r, status, page, kit.Page{User: &user, Nav: "settings", Title: title}, d,
		kit.Failure{Err: viewErr, Form: "category"})
}

// categoryNameError returns the error of the category name input, nil if name is valid.
func categoryNameError(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return domain.NewError(errors.ErrInvalid, "required")
	case utf8.RuneCountInString(name) > domain.MaxTextLen:
		return domain.NewError(errors.ErrInvalid, "too long")
	case strings.Contains(name, " / "):
		return domain.NewError(errors.ErrInvalid, "must not contain %q", " / ")
	}
	return nil
}

func (h *Handler) categories(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := categoriesData{Kind: domain.Kind(r.URL.Query().Get("kind"))}
	return h.renderCategories(w, r, user, categoriesPage, "Categories", http.StatusOK, d, nil)
}

func (h *Handler) newCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := categoriesData{Kind: domain.Kind(r.URL.Query().Get("kind"))}
	return h.renderCategories(w, r, user, newCategoryPage, "New category", http.StatusOK, d, nil)
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var d categoriesData
	if err := h.app.DecodeForm(r, &d); err != nil {
		return err
	}
	if err := categoryNameError(d.Name); err != nil {
		return h.renderCategories(
			w,
			r,
			user,
			newCategoryPage,
			"New category",
			http.StatusUnprocessableEntity,
			d,
			kit.FieldsError{"name": err},
		)
	}
	c, err := h.app.Svc.Categories.Create(r.Context(), user.ID, d.Parent, d.Kind, d.Name)
	if kit.IsUserError(err) {
		return h.renderCategories(w, r, user, newCategoryPage, "New category", http.StatusUnprocessableEntity, d, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/categories?kind="+string(c.Kind))
	return nil
}

// renderCategory renders the page of category id. A nil f shows the category's values
// in the form, otherwise f holds the values of a form post that failed with viewErr.
func (h *Handler) renderCategory(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, f *categoryData, viewErr error,
) error {
	c, tree, err := h.app.Svc.Categories.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	d := categoryData{Category: c, Name: c.Name, Parent: c.ParentID}
	if f != nil {
		d.Name, d.Parent = f.Name, f.Parent
	}
	d.Parents, d.ParentName = domain.ParentChoices(tree, c)
	return kit.Render(h.app, w, r, status, categoryPage, kit.Page{
		User: &user, Nav: "settings", Title: "%s", TitleArgs: []any{c.Name},
	}, d, kit.Failure{Err: viewErr, Form: "category"})
}

func (h *Handler) category(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	return h.renderCategory(w, r, user, id, http.StatusOK, nil, nil)
}

func (h *Handler) updateCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	var f categoryData
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	if err := categoryNameError(f.Name); err != nil {
		return h.renderCategory(w, r, user, id, http.StatusUnprocessableEntity, &f, kit.FieldsError{"name": err})
	}
	err = h.app.Svc.Categories.Update(r.Context(), user.ID, id, f.Name, f.Parent)
	if kit.IsUserError(err) {
		return h.renderCategory(w, r, user, id, http.StatusUnprocessableEntity, &f, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/categories")
	return nil
}

func (h *Handler) archiveCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err := h.app.Svc.Categories.Archive(r.Context(), user.ID, id, r.PostFormValue("archived") == "1"); err != nil {
		return err
	}
	kit.Redirect(w, r, "/categories/"+r.PathValue("id"))
	return nil
}

func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Categories.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	kit.Redirect(w, r, "/categories")
	return nil
}
