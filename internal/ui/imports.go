package ui

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
)

type importsData struct {
	Imports []domain.Import
}

func (u *UI) imports(w http.ResponseWriter, r *http.Request, user domain.User) error {
	imports, err := u.svc.Imports.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	return u.render(w, r, http.StatusOK, "imports", view{
		User: &user, Nav: "settings", Title: "Import", Data: importsData{Imports: imports},
	})
}

type importNewData struct {
	// Step is "type" or "file".
	Step   string
	Source domain.ImportSource
	// Rates is set instead of Source for a file of exchange rates.
	Rates bool
	// Header is the header line of a file of Source.
	Header string
}

// _ratesSource is the source query value of a file of exchange rates; they are not
// operations, so it is no domain.ImportSource.
const _ratesSource = "rates"

func (u *UI) importNew(w http.ResponseWriter, r *http.Request, user domain.User) error {
	d := importNewData{Step: "type"}
	if src, err := domain.ParseImportSource(r.URL.Query().Get("source")); err == nil {
		d = importNewData{Step: "file", Source: src, Header: u.svc.Imports.Header(src)}
	} else if r.URL.Query().Get("source") == _ratesSource {
		d = importNewData{Step: "file", Rates: true, Header: importer.RatesHeader}
	}
	return u.render(w, r, http.StatusOK, "import_new", view{
		User: &user, Nav: "settings", Title: "New import", Data: d,
	})
}

func (u *UI) upload(w http.ResponseWriter, r *http.Request, user domain.User) error {
	// parsed here, since FormValue drops the error of a body over u.maxUpload
	err := r.ParseMultipartForm(u.maxUpload) //nolint:gosec // authed limits the body to u.maxUpload
	if errors.Is(err, http.ErrNotMultipart) {
		err = r.ParseForm()
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return domain.NewError(errors.ErrInvalid, "file: larger than %d MB", u.maxUpload>>20)
	}
	if err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	src, err := domain.ParseImportSource(r.FormValue("source"))
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "type: choose what to import")
	}
	var (
		file     io.Reader
		filename string
	)
	f, hdr, err := r.FormFile("file")
	switch {
	case errors.Is(err, http.ErrMissingFile) || errors.Is(err, http.ErrNotMultipart):
		content := strings.TrimSpace(r.FormValue("content"))
		if content == "" {
			return domain.NewError(errors.ErrInvalid, "file: choose a CSV file or paste its content")
		}
		file, filename = strings.NewReader(content), "pasted.csv"
	case err != nil:
		return fmt.Errorf("file: %w: %w", err, errors.ErrInvalid)
	default:
		defer f.Close()
		file, filename = f, filepath.Base(hdr.Filename)
	}
	im, err := u.svc.Imports.Upload(r.Context(), user.ID, src, filename, file, user.Location())
	if err != nil {
		return err
	}
	redirect(w, r, "/imports/"+strconv.FormatInt(im.ID, 10))
	return nil
}

type importData struct {
	// Step is "accounts", "categories" or "preview".
	Step       string
	Preview    domain.Preview
	Accounts   []domain.Account
	Categories []domain.Category
	// AccountKinds are offered for new accounts.
	AccountKinds []domain.AccountKind
}

func (u *UI) importPage(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	p, err := u.svc.Imports.Preview(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	base := "/imports/" + r.PathValue("id")
	staged := p.Import.Status == domain.ImportStaged
	d := importData{Step: r.PathValue("step"), Preview: p}
	switch d.Step {
	case "":
		// continue at the first unfinished step
		switch {
		case !staged:
			redirect(w, r, base+"/preview")
		case !p.AccountsMapped():
			redirect(w, r, base+"/accounts")
		case !p.CategoriesMapped():
			redirect(w, r, base+"/categories")
		default:
			redirect(w, r, base+"/preview")
		}
		return nil
	case "accounts":
		if !staged {
			redirect(w, r, base+"/preview")
			return nil
		}
		if d.Accounts, err = u.svc.Accounts.List(r.Context(), user.ID); err != nil {
			return err
		}
		d.Preview = p.Suggest(d.Accounts, nil)
		d.AccountKinds = domain.AccountKinds()
	case "categories":
		if !staged {
			redirect(w, r, base+"/preview")
			return nil
		}
		if d.Categories, err = u.svc.Categories.Tree(r.Context(), user.ID); err != nil {
			return err
		}
		d.Preview = p.Suggest(nil, d.Categories)
	case "preview":
	default:
		return errors.ErrNotFound
	}
	return u.render(w, r, http.StatusOK, "import", view{
		User: &user, Nav: "settings", Title: "Import #%s", TitleArgs: []any{r.PathValue("id")}, Data: d,
	})
}

func (u *UI) saveAccounts(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Names      []string `form:"acc_name"`
		Currencies []string `form:"acc_currency"`
		IDs        []int64  `form:"acc_id"` // 0 means create
		NewNames   []string `form:"acc_new"`
		NewKinds   []string `form:"acc_kind"`
		NewLimits  []string `form:"acc_limit"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	n := len(f.Names)
	if len(f.Currencies) != n || len(f.IDs) != n || len(f.NewNames) != n || len(f.NewKinds) != n ||
		len(f.NewLimits) != n {
		return domain.NewError(errors.ErrInvalid, "form: account fields do not match")
	}
	accounts := make([]domain.AccountChoice, 0, n)
	for i, name := range f.Names {
		c := domain.AccountChoice{Name: name, Currency: f.Currencies[i], AccountID: f.IDs[i]}
		if c.AccountID == 0 {
			c.NewName = f.NewNames[i]
			c.NewKind = domain.AccountKind(f.NewKinds[i])
			s := strings.TrimSpace(f.NewLimits[i])
			if s != "" && c.NewKind == domain.AccountCredit {
				if c.NewCreditLimit, err = money.Parse(s); err != nil {
					return domain.NewError(errors.ErrInvalid, "%s: credit limit: enter a number", name)
				}
			}
		}
		accounts = append(accounts, c)
	}
	if err := u.svc.Imports.SaveMapping(r.Context(), user.ID, id, accounts, nil); err != nil {
		return err
	}
	redirect(w, r, "/imports/"+r.PathValue("id")+"/categories")
	return nil
}

func (u *UI) saveCategories(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Names    []string `form:"cat_name"`
		Kinds    []string `form:"cat_kind"`
		IDs      []int64  `form:"cat_id"` // 0 means create
		NewNames []string `form:"cat_new"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	n := len(f.Names)
	if len(f.Kinds) != n || len(f.IDs) != n || len(f.NewNames) != n {
		return domain.NewError(errors.ErrInvalid, "form: category fields do not match")
	}
	categories := make([]domain.CategoryChoice, 0, n)
	for i, name := range f.Names {
		c := domain.CategoryChoice{Name: name, Kind: domain.Kind(f.Kinds[i]), CategoryID: f.IDs[i]}
		if c.CategoryID == 0 {
			// "Parent / Child" creates a nested category
			parent, child, _ := strings.Cut(f.NewNames[i], " / ")
			c.Cat1, c.Cat2 = strings.TrimSpace(parent), strings.TrimSpace(child)
		}
		categories = append(categories, c)
	}
	if err := u.svc.Imports.SaveMapping(r.Context(), user.ID, id, nil, categories); err != nil {
		return err
	}
	redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (u *UI) unpair(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	row, err := strconv.ParseInt(r.PathValue("row"), 10, 64)
	if err != nil || row <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Imports.Unpair(r.Context(), user.ID, id, row); err != nil {
		return err
	}
	redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (u *UI) commit(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if _, err := u.svc.Imports.Commit(r.Context(), user.ID, id, user.Location()); err != nil {
		return err
	}
	redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (u *UI) undo(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if _, err := u.svc.Imports.Undo(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/imports/"+r.PathValue("id")+"/preview")
	return nil
}

func (u *UI) discard(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Imports.Discard(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/imports")
	return nil
}

type importRatesData struct {
	Changes []domain.RateChange
	// Content is the uploaded file, sent again to commit it.
	Content string
	New     int
	Changed int
	// Imported is the number of stored rates once committed.
	Imported int
	Done     bool
}

// previewRates shows the daily rates a file of exchange rates would store. Nothing is
// staged: the confirm form sends the file content again.
func (u *UI) previewRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	// parsed here, since FormValue drops the error of a body over u.maxUpload
	err := r.ParseMultipartForm(u.maxUpload) //nolint:gosec // authed limits the body to u.maxUpload
	if errors.Is(err, http.ErrNotMultipart) {
		err = r.ParseForm()
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return domain.NewError(errors.ErrInvalid, "file: larger than %d MB", u.maxUpload>>20)
	}
	if err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var content string
	f, _, err := r.FormFile("file")
	switch {
	case errors.Is(err, http.ErrMissingFile) || errors.Is(err, http.ErrNotMultipart):
		content = strings.TrimSpace(r.FormValue("content"))
		if content == "" {
			return domain.NewError(errors.ErrInvalid, "file: choose a CSV file or paste its content")
		}
	case err != nil:
		return fmt.Errorf("file: %w: %w", err, errors.ErrInvalid)
	default:
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		content = string(b)
	}
	changes, err := u.svc.Rates.Preview(r.Context(), user.ID, strings.NewReader(content))
	if err != nil {
		return err
	}
	d := importRatesData{Changes: changes, Content: content}
	for _, c := range changes {
		switch c.Status() {
		case "new":
			d.New++
		case "changed":
			d.Changed++
		}
	}
	return u.render(w, r, http.StatusOK, "import_rates", view{
		User: &user, Nav: "settings", Title: "Import exchange rates", Data: d,
	})
}

func (u *UI) commitRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	n, err := u.svc.Rates.Import(r.Context(), user.ID, strings.NewReader(r.PostFormValue("content")))
	if err != nil {
		return err
	}
	return u.render(w, r, http.StatusOK, "import_rates", view{
		User: &user, Nav: "settings", Title: "Import exchange rates",
		Data: importRatesData{Imported: n, Done: true},
	})
}
