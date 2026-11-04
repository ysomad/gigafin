// Package i18n translates the UI. Messages are keyed by their English text, so English
// needs no dictionary and a missing translation shows the English text. A key may end
// with a context after "|", such as "May|date", to translate the same English text
// differently; the context is dropped when there is no translation.
//
// Plurals are not supported yet: when a message needs them, make its dictionary value
// an object of CLDR forms and pick one with golang.org/x/text/feature/plural.
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"golang.org/x/text/language"
)

// Language is a UI language.
type Language struct {
	// Code is the BCP 47 code, such as "ru".
	Code string
	// Name is the name of the language in itself.
	Name string
}

// _languages are the UI languages, English, the default and the language of the keys,
// first.
var _languages = []Language{{Code: "en", Name: "English"}, {Code: "ru", Name: "Русский"}}

// _matcher matches browser languages against _languages, in the same order.
var _matcher = func() language.Matcher {
	tags := make([]language.Tag, len(_languages))
	for i, l := range _languages {
		tags[i] = language.MustParse(l.Code)
	}
	return language.NewMatcher(tags)
}()

// Languages returns the UI languages, the default first.
func Languages() []Language {
	return slices.Clone(_languages)
}

// Dicts maps a language code to translations keyed by the English text.
type Dicts map[string]map[string]string

// Load reads locales/<code>.json from fsys for every language but English.
func Load(fsys fs.FS) (Dicts, error) {
	d := make(Dicts, len(_languages))
	for _, l := range _languages[1:] {
		b, err := fs.ReadFile(fsys, "locales/"+l.Code+".json")
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", l.Code, err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("parse %s: %w", l.Code, err)
		}
		d[l.Code] = m
	}
	return d, nil
}

// T returns key translated to lang and formatted with args, if any.
func (d Dicts) T(lang, key string, args ...any) string {
	if s, ok := d[lang][key]; ok {
		key = s
	} else if i := strings.LastIndexByte(key, '|'); i >= 0 {
		key = key[:i]
	}
	if len(args) == 0 {
		return key
	}
	return fmt.Sprintf(key, args...)
}

// Match returns the UI language that best fits an Accept-Language header, the default
// if none does.
func Match(acceptLanguage string) string {
	// a malformed header gives no tags, which match the default
	tags, _, _ := language.ParseAcceptLanguage(acceptLanguage) //nolint:errcheck // see above
	_, i, _ := _matcher.Match(tags...)
	return _languages[i].Code
}
