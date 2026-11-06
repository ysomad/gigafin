// Package i18n translates the UI. Messages are keyed by their English text, so English
// needs no dictionary and a missing translation shows the English text. A key may end
// with a context after "|", such as "May|date", to translate the same English text
// differently; the context is dropped when there is no translation.
//
// A dictionary, locales/<code>.json, groups its messages by area of the UI, as
// locales/schema.json describes:
//
//	{
//	  "$schema": "./schema.json",
//	  "common": {"Save": "Сохранить"},
//	  "imports": {"%d rows": {"one": "%d строка", "few": "%d строки", "many": "%d строк", "other": "%d строки"}}
//	}
//
// Groups only organize the file: a key is translated the same wherever it is used and
// may be in one group only. A message is a string or, when it counts something, an
// object of CLDR plural forms (zero, one, two, few, many, other; other is required)
// picked by the first integer arg. English has no dictionary but may have
// locales/en.json for the plural forms of its keys. A translation reorders args with
// explicit indexes, such as "%[2]s: %[1]d".
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"

	"github.com/ysomad/gigafin/internal/errors"
)

// Language is a UI language.
type Language struct {
	Code string
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

// _forms are the plural forms by their names in a dictionary.
var _forms = map[string]plural.Form{
	"zero": plural.Zero, "one": plural.One, "two": plural.Two,
	"few": plural.Few, "many": plural.Many, "other": plural.Other,
}

// Languages returns the UI languages, the default first.
func Languages() []Language {
	return slices.Clone(_languages)
}

// Message is a translation: Text, or Forms when it has plural forms.
type Message struct {
	Text  string
	Forms map[plural.Form]string
}

// Dicts maps a language code to translations keyed by the English text.
type Dicts map[string]map[string]Message

// Load reads locales/<code>.json from fsys for every language, optional for English.
func Load(fsys fs.FS) (Dicts, error) {
	d := make(Dicts, len(_languages))
	for i, l := range _languages {
		b, err := fs.ReadFile(fsys, "locales/"+l.Code+".json")
		if i == 0 && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", l.Code, err)
		}
		var groups map[string]json.RawMessage
		if err := json.Unmarshal(b, &groups); err != nil {
			return nil, fmt.Errorf("parse %s: %w", l.Code, err)
		}
		delete(groups, "$schema")
		msgs := map[string]Message{}
		groupOf := map[string]string{}
		for g, group := range groups {
			var raws map[string]json.RawMessage
			if err := json.Unmarshal(group, &raws); err != nil {
				return nil, fmt.Errorf("parse %s: %s: %w", l.Code, g, err)
			}
			for k, raw := range raws {
				if prev, ok := groupOf[k]; ok {
					return nil, fmt.Errorf("%s: %q is in groups %s and %s", l.Code, k, prev, g)
				}
				groupOf[k] = g
				var m Message
				if err := json.Unmarshal(raw, &m.Text); err != nil {
					var forms map[string]string
					if err := json.Unmarshal(raw, &forms); err != nil {
						return nil, fmt.Errorf("%s: %s: %q: not a string or plural forms", l.Code, g, k)
					}
					if _, ok := forms["other"]; !ok {
						return nil, fmt.Errorf("%s: %s: %q: no other plural form", l.Code, g, k)
					}
					m.Forms = make(map[plural.Form]string, len(forms))
					for name, s := range forms {
						f, ok := _forms[name]
						if !ok {
							return nil, fmt.Errorf("%s: %s: %q: unknown plural form %q", l.Code, g, k, name)
						}
						m.Forms[f] = s
					}
				}
				msgs[k] = m
			}
		}
		d[l.Code] = msgs
	}
	return d, nil
}

// T returns key translated to lang and formatted with args, if any.
func (d Dicts) T(lang, key string, args ...any) string {
	m, ok := d[lang][key]
	switch {
	case m.Forms != nil:
		key = m.Forms[plural.Other]
		if n, ok := firstInt(args); ok {
			if s, ok := m.Forms[plural.Cardinal.MatchPlural(language.Make(lang), n, 0, 0, 0, 0)]; ok {
				key = s
			}
		}
	case ok:
		key = m.Text
	default:
		if i := strings.LastIndexByte(key, '|'); i >= 0 {
			key = key[:i]
		}
	}
	if len(args) == 0 {
		return key
	}
	return fmt.Sprintf(key, args...)
}

// firstInt returns the absolute value of the first integer of args, modulo 10,000,000
// as plural.Rules.MatchPlural takes it.
func firstInt(args []any) (int, bool) {
	for _, a := range args {
		var n int64
		switch a := a.(type) {
		case int:
			n = int64(a)
		case int64:
			n = a
		case int32:
			n = int64(a)
		default:
			continue
		}
		if n < 0 {
			n = -n
		}
		return int(n % 10_000_000), true
	}
	return 0, false
}

// Match returns the UI language that best fits an Accept-Language header, the default
// if none does.
func Match(acceptLanguage string) string {
	// a malformed header gives no tags, which match the default
	tags, _, _ := language.ParseAcceptLanguage(acceptLanguage) //nolint:errcheck // see above
	_, i, _ := _matcher.Match(tags...)
	return _languages[i].Code
}
