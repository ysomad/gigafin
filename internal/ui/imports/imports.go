package imports

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the imports of operations and exchange rates.
type Handler struct {
	app *kit.App
}

// New returns the handler of imports.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Routes returns the routes of imports.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /imports":                    h.imports,
		"POST /imports":                   h.upload,
		"POST /imports/rates":             h.previewRates,
		"POST /imports/rates/commit":      h.commitRates,
		"GET /imports/new":                h.importNew,
		"GET /imports/{id}":               h.importPage,
		"GET /imports/{id}/{step}":        h.importPage,
		"POST /imports/{id}/accounts":     h.saveAccounts,
		"POST /imports/{id}/categories":   h.saveCategories,
		"POST /imports/{id}/unpair/{row}": h.unpair,
		"POST /imports/{id}/commit":       h.commit,
		"POST /imports/{id}/undo":         h.undo,
		"POST /imports/{id}/discard":      h.discard,
	}
}

func (h *Handler) imports(w http.ResponseWriter, r *http.Request, user domain.User) error {
	imports, err := h.app.Svc.Imports.List(r.Context(), user.ID)
	if err != nil {
		return err
	}

	return kit.Render(h.app, w, r, http.StatusOK, listPage, kit.Page{User: &user, Nav: "settings", Title: "Import"},
		listData{Imports: imports}, kit.Failure{})
}

// _ratesSource is the source query value of a file of exchange rates; they are not
// operations, so it is no domain.ImportSource.
const _ratesSource = "rates"

func (h *Handler) importNew(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := formData{Step: "type"}

	if src, err := domain.ParseImportSource(r.URL.Query().Get("source")); err == nil {
		d = formData{Step: "file", Source: src, Header: h.app.Svc.Imports.Header(src)}
	} else if r.URL.Query().Get("source") == _ratesSource {
		d = formData{Step: "file", Rates: true, Header: importer.RatesHeader}
	}

	return kit.Render(h.app, w, r, http.StatusOK, formPage, kit.Page{User: &user, Nav: "settings", Title: "New import"},
		d, kit.Failure{})
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request, user domain.User) error {
	content, filename, pasted, err := h.readUpload(r)
	if err != nil {
		return err
	}
	src, err := domain.ParseImportSource(r.FormValue("source"))
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "choose what to import")
	}
	d := formData{Step: "file", Source: src, Header: h.app.Svc.Imports.Header(src), Content: pasted}
	page := kit.Page{User: &user, Nav: "settings", Title: "New import"}
	if content == "" {
		errs := kit.FieldsError{"file": domain.NewError(errors.ErrInvalid, "choose a CSV file or paste its content")}
		return kit.Render(h.app, w, r, http.StatusUnprocessableEntity, formPage, page, d,
			kit.Failure{Err: errs, Form: "import"})
	}
	im, err := h.app.Svc.Imports.Upload(r.Context(), user.ID, src, filename, strings.NewReader(content), user.Location())
	if kit.IsUserError(err) {
		return kit.Render(h.app, w, r, http.StatusUnprocessableEntity, formPage, page, d,
			kit.Failure{Err: err, Form: "import"})
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/imports/"+strconv.FormatInt(im.ID, 10))
	return nil
}

// readUpload parses the posted import form and returns the CSV it carries: the content
// of the file chosen and its name, else the content pasted, empty when there is neither.
// pasted is the content pasted, to fill the form again.
func (h *Handler) readUpload(r *http.Request) (content, filename, pasted string, err error) {
	// parsed here, since FormValue drops the error of a body over h.app.MaxUpload
	err = r.ParseMultipartForm(h.app.MaxUpload) //nolint:gosec // authed limits the body to h.app.MaxUpload
	if errors.Is(err, http.ErrNotMultipart) {
		err = r.ParseForm()
	}
	// the form is lost with a body too large, so its error is a toast
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return "", "", "", domain.NewError(errors.ErrInvalid, "the file is larger than %d MB", h.app.MaxUpload>>20)
	}
	if err != nil {
		return "", "", "", domain.NewError(errors.ErrInvalid, "invalid form")
	}
	pasted = strings.TrimSpace(r.FormValue("content"))
	f, hdr, err := r.FormFile("file")
	switch {
	case errors.Is(err, http.ErrMissingFile) || errors.Is(err, http.ErrNotMultipart):
		return pasted, "pasted.csv", pasted, nil
	case err != nil:
		return "", "", "", fmt.Errorf("file: %w: %w", err, errors.ErrInvalid)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return "", "", "", fmt.Errorf("read file: %w", err)
	}
	return string(b), filepath.Base(hdr.Filename), pasted, nil
}

func (h *Handler) importPage(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	p, err := h.app.Svc.Imports.Preview(r.Context(), user.ID, id)
	if err != nil {
		return err
	}

	base := "/imports/" + r.PathValue("id")
	staged := p.Import.Status == domain.ImportStaged
	d := wizardData{Step: r.PathValue("step"), Preview: p}

	switch d.Step {
	case "":
		// continue at the first unfinished step
		switch {
		case !staged:
			kit.Redirect(w, r, base+"/preview")
		case !p.AccountsMapped():
			kit.Redirect(w, r, base+"/accounts")
		case !p.CategoriesMapped():
			kit.Redirect(w, r, base+"/categories")
		default:
			kit.Redirect(w, r, base+"/preview")
		}
		return nil
	case "accounts":
		if !staged {
			kit.Redirect(w, r, base+"/preview")
			return nil
		}
		if d.Accounts, err = h.app.Svc.Accounts.List(r.Context(), user.ID); err != nil {
			return err
		}
		d.Preview = p.Suggest(d.Accounts, nil)
		d.AccountKinds = domain.AccountKinds()
	case "categories":
		if !staged {
			kit.Redirect(w, r, base+"/preview")
			return nil
		}
		if d.Categories, err = h.app.Svc.Categories.Tree(r.Context(), user.ID); err != nil {
			return err
		}
		d.Preview = p.Suggest(nil, d.Categories)
	case "preview":
	default:
		return errors.ErrNotFound
	}

	return kit.Render(h.app, w, r, http.StatusOK, wizardPage, kit.Page{
		User: &user, Nav: "settings", Title: "Import #%s", TitleArgs: []any{r.PathValue("id")},
	}, d, kit.Failure{})
}

func (h *Handler) saveAccounts(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	var form struct {
		Names      []string `form:"acc_name"`
		Currencies []string `form:"acc_currency"`
		IDs        []int64  `form:"acc_id"` // 0 means create
		NewNames   []string `form:"acc_new"`
		NewKinds   []string `form:"acc_kind"`
		NewLimits  []string `form:"acc_limit"`
	}
	if err := h.app.DecodeForm(r, &form); err != nil {
		return err
	}

	n := len(form.Names)
	if len(form.Currencies) != n || len(form.IDs) != n || len(form.NewNames) != n || len(form.NewKinds) != n ||
		len(form.NewLimits) != n {
		return domain.NewError(errors.ErrInvalid, "account fields do not match")
	}

	accounts := make([]domain.AccountChoice, 0, n)
	for i, name := range form.Names {
		c := domain.AccountChoice{Name: name, Currency: form.Currencies[i], AccountID: form.IDs[i]}

		if c.AccountID == 0 {
			c.NewName = form.NewNames[i]
			c.NewKind = domain.AccountKind(form.NewKinds[i])
			s := strings.TrimSpace(form.NewLimits[i])
			if s != "" && c.NewKind == domain.AccountCredit {
				if c.NewCreditLimit, err = money.Parse(s); err != nil {
					return domain.NewError(errors.ErrInvalid, "%s: credit limit: enter a number", name)
				}
			}
		}

		accounts = append(accounts, c)
	}

	if err := h.app.Svc.Imports.SaveMapping(r.Context(), user.ID, id, accounts, nil); err != nil {
		return err
	}

	kit.Redirect(w, r, "/imports/"+r.PathValue("id")+"/categories")
	return nil
}

func (h *Handler) saveCategories(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	var form struct {
		Names    []string `form:"cat_name"`
		Kinds    []string `form:"cat_kind"`
		IDs      []int64  `form:"cat_id"` // 0 means create
		NewNames []string `form:"cat_new"`
	}
	if err := h.app.DecodeForm(r, &form); err != nil {
		return err
	}

	n := len(form.Names)
	if len(form.Kinds) != n || len(form.IDs) != n || len(form.NewNames) != n {
		return domain.NewError(errors.ErrInvalid, "category fields do not match")
	}

	categories := make([]domain.CategoryChoice, 0, n)
	for i, name := range form.Names {
		c := domain.CategoryChoice{Name: name, Kind: domain.Kind(form.Kinds[i]), CategoryID: form.IDs[i]}
		if c.CategoryID == 0 {
			// "Parent / Child" creates a nested category
			parent, child, _ := strings.Cut(form.NewNames[i], " / ")
			c.Cat1, c.Cat2 = strings.TrimSpace(parent), strings.TrimSpace(child)
		}
		categories = append(categories, c)
	}

	if err := h.app.Svc.Imports.SaveMapping(r.Context(), user.ID, id, nil, categories); err != nil {
		return err
	}

	kit.Redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (h *Handler) unpair(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	row, err := kit.PathID(r, "row")
	if err != nil {
		return err
	}

	if err := h.app.Svc.Imports.Unpair(r.Context(), user.ID, id, row); err != nil {
		return err
	}

	kit.Redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	if _, err := h.app.Svc.Imports.Commit(r.Context(), user.ID, id, user.Location()); err != nil {
		return err
	}

	kit.Redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (h *Handler) undo(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	if _, err := h.app.Svc.Imports.Undo(r.Context(), user.ID, id); err != nil {
		return err
	}

	kit.Redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (h *Handler) discard(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}

	if err := h.app.Svc.Imports.Discard(r.Context(), user.ID, id); err != nil {
		return err
	}

	kit.Redirect(w, r, "/imports")
	return nil
}

// previewRates shows the daily rates a file of exchange rates would store. Nothing is
// staged: the confirm form sends the file content again.
func (h *Handler) previewRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	content, _, pasted, err := h.readUpload(r)
	if err != nil {
		return err
	}
	d := formData{Step: "file", Rates: true, Header: importer.RatesHeader, Content: pasted}
	page := kit.Page{User: &user, Nav: "settings", Title: "New import"}
	if content == "" {
		errs := kit.FieldsError{"file": domain.NewError(errors.ErrInvalid, "choose a CSV file or paste its content")}
		return kit.Render(h.app, w, r, http.StatusUnprocessableEntity, formPage, page, d,
			kit.Failure{Err: errs, Form: "import-rates"})
	}
	changes, err := h.app.Svc.Rates.Preview(r.Context(), user.ID, strings.NewReader(content))
	if kit.IsUserError(err) {
		return kit.Render(h.app, w, r, http.StatusUnprocessableEntity, formPage, page, d,
			kit.Failure{Err: err, Form: "import-rates"})
	}
	if err != nil {
		return err
	}
	rd := ratesData{Changes: changes, Content: content}
	for _, c := range changes {
		switch c.Status() {
		case "new":
			rd.New++
		case "changed":
			rd.Changed++
		}
	}
	return kit.Render(h.app, w, r, http.StatusOK, ratesPage, kit.Page{
		User: &user, Nav: "settings", Title: "Import exchange rates",
	}, rd, kit.Failure{})
}

func (h *Handler) commitRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	n, err := h.app.Svc.Rates.Import(r.Context(), user.ID, strings.NewReader(r.PostFormValue("content")))
	if err != nil {
		return err
	}
	return kit.Render(h.app, w, r, http.StatusOK, ratesPage, kit.Page{
		User: &user, Nav: "settings", Title: "Import exchange rates",
	}, ratesData{Imported: n, Done: true}, kit.Failure{})
}
