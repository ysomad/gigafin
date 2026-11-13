package imports

import (
	"github.com/a-h/templ"

	"github.com/ysomad/gigafin/internal/domain"
)

type listData struct {
	Imports []domain.Import
}

type formData struct {
	// Step is "type" or "file".
	Step   string
	Source domain.ImportSource
	// Rates is set instead of Source for a file of exchange rates.
	Rates bool
	// Header is the header line of a file of Source.
	Header string
	// Content is the pasted content of an upload that failed, to fill it again.
	Content string
}

type wizardData struct {
	// Step is "accounts", "categories" or "preview".
	Step       string
	Preview    domain.Preview
	Accounts   []domain.Account
	Categories []domain.Category
	// AccountKinds are offered for new accounts.
	AccountKinds []domain.AccountKind
}

type ratesData struct {
	Changes []domain.RateChange
	// Content is the uploaded file, sent again to commit it.
	Content string
	New     int
	Changed int
	// Imported is the number of stored rates once committed.
	Imported int
	Done     bool
}

// statusVariant is the badge variant of an import of status: none while staged.
func statusVariant(status domain.ImportStatus) templ.Attributes {
	switch status {
	case domain.ImportStaged:
		return nil
	case domain.ImportCommitted:
		return templ.Attributes{"data-variant": "success"}
	default:
		return templ.Attributes{"data-variant": "secondary"}
	}
}
