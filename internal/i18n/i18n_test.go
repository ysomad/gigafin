package i18n_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/web"
)

func TestT(t *testing.T) {
	t.Parallel()

	d := i18n.Dicts{"ru": {"Save": {Text: "Сохранить"}, "Rate, %s": {Text: "Курс, %s"}}}
	assert.Equal(t, "Сохранить", d.T("ru", "Save"))
	assert.Equal(t, "Save", d.T("en", "Save"))
	assert.Equal(t, "Missing", d.T("ru", "Missing"))
	assert.Equal(t, "Курс, USD", d.T("ru", "Rate, %s", "USD"))
	assert.Equal(t, "Rate, USD", d.T("en", "Rate, %s", "USD"))
	assert.Equal(t, "May", d.T("en", "May|date"))
}

func TestTPlural(t *testing.T) {
	t.Parallel()

	d, err := i18n.Load(fstest.MapFS{
		"locales/en.json": {Data: []byte(`{"g": {"%d rows": {"one": "%d row", "other": "%d rows"}}}`)},
		"locales/ru.json": {Data: []byte(`{"g": {"%d rows": {
			"one": "%d строка", "few": "%d строки", "many": "%d строк", "other": "%d строки"}}}`)},
	})
	require.NoError(t, err)
	tests := []struct {
		lang string
		n    any
		want string
	}{
		{lang: "en", n: 1, want: "1 row"},
		{lang: "en", n: 2, want: "2 rows"},
		{lang: "en", n: 0, want: "0 rows"},
		{lang: "ru", n: 1, want: "1 строка"},
		{lang: "ru", n: 2, want: "2 строки"},
		{lang: "ru", n: 5, want: "5 строк"},
		{lang: "ru", n: 11, want: "11 строк"},
		{lang: "ru", n: 21, want: "21 строка"},
		{lang: "ru", n: int64(22), want: "22 строки"},
		{lang: "ru", n: -5, want: "-5 строк"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, d.T(tt.lang, "%d rows", tt.n), "%s %v", tt.lang, tt.n)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"duplicate key":   `{"a": {"Save": "Сохранить"}, "b": {"Save": "Сохранить"}}`,
		"no other form":   `{"a": {"%d rows": {"one": "%d строка"}}}`,
		"unknown form":    `{"a": {"%d rows": {"single": "%d строка", "other": "%d строки"}}}`,
		"not grouped":     `{"Save": "Сохранить"}`,
		"invalid message": `{"a": {"Save": 1}}`,
	}
	for name, ru := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := i18n.Load(fstest.MapFS{"locales/ru.json": {Data: []byte(ru)}})
			assert.Error(t, err)
		})
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		accept string
		want   string
	}{
		{accept: "ru-RU,ru;q=0.9,en-US;q=0.8", want: "ru"},
		{accept: "en-US,en;q=0.9,ru;q=0.8", want: "en"},
		{accept: "de-DE", want: "en"},
		{accept: "", want: "en"},
		{accept: ";;garbage", want: "en"},
	}
	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, i18n.Match(tt.accept))
		})
	}
}

// TestRussianCoverage checks that ru.json translates every key of the templates and of
// the Go code: t calls, page titles, domain.NewError formats and domain.Msg constants.
func TestRussianCoverage(t *testing.T) {
	t.Parallel()

	d, err := i18n.Load(web.FS)
	require.NoError(t, err)
	ru := d["ru"]

	keys := templateKeys(t)
	maps.Copy(keys, goKeys(t))
	require.NotEmpty(t, keys)

	letter := regexp.MustCompile(`[A-Za-z]`)
	var missing []string
	for k, where := range keys {
		if _, ok := ru[k]; !ok && letter.MatchString(strings.ReplaceAll(k, "%s", "")) {
			missing = append(missing, strconv.Quote(k)+" at "+where)
		}
	}
	slices.Sort(missing)
	assert.Empty(t, missing, "untranslated keys")
}

// templateKeys returns the keys of t calls in the templates, mapped to the template.
func templateKeys(t *testing.T) map[string]string {
	t.Helper()
	keys := map[string]string{}
	re := regexp.MustCompile("[{(]-?\\s*t\\s+(\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`)")
	tmpls, err := fs.Glob(web.FS, "templates/*.html")
	require.NoError(t, err)
	for _, name := range tmpls {
		b, err := fs.ReadFile(web.FS, name)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			k, err := strconv.Unquote(m[1])
			require.NoError(t, err, name)
			keys[k] = name
		}
	}
	return keys
}

// goKeys returns the literal keys of the Go code, mapped to their position: formats
// of NewError, keys of t calls, page titles and Msg constants.
func goKeys(t *testing.T) map[string]string {
	t.Helper()
	keys := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join("..", "..", "internal"), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() && e.Name() == "gen" {
			return filepath.SkipDir
		}
		if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			for _, e := range keyLits(n) {
				if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
					k, err := strconv.Unquote(b.Value)
					require.NoError(t, err)
					keys[k] = fset.Position(b.Pos()).String()
				}
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	return keys
}

// keyLits returns the key arguments of n if n is a NewError, t or Msg call, a Title
// field or a declaration of Msg constants.
func keyLits(n ast.Node) []ast.Expr {
	switch n := n.(type) {
	case *ast.CallExpr:
		var name string
		switch fn := n.Fun.(type) {
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		case *ast.Ident:
			name = fn.Name
		}
		switch {
		case name == "NewError" && len(n.Args) >= 2:
			return n.Args[1:2]
		case (name == "t" || name == "Msg") && len(n.Args) >= 1:
			return n.Args[:1]
		}
	case *ast.KeyValueExpr:
		if k, ok := n.Key.(*ast.Ident); ok && k.Name == "Title" {
			return []ast.Expr{n.Value}
		}
	case *ast.ValueSpec:
		if s, ok := n.Type.(*ast.SelectorExpr); ok && s.Sel.Name == "Msg" {
			return n.Values
		}
		if id, ok := n.Type.(*ast.Ident); ok && id.Name == "Msg" {
			return n.Values
		}
	}
	return nil
}

// _runtimeGroups hold keys that TestUnused cannot find in the code: built at runtime,
// such as enum values and month names, or passed in variables. TestRuntimeKeys checks
// those it can list.
var _runtimeGroups = []string{"dates", "values"}

// TestUnused checks that every key of the locale files, but those of _runtimeGroups, is
// used by the templates or the Go code.
func TestUnused(t *testing.T) {
	t.Parallel()

	keys := templateKeys(t)
	maps.Copy(keys, goKeys(t))
	var unused []string
	for name, b := range localeFiles(t) {
		var groups map[string]any
		require.NoError(t, json.Unmarshal(b, &groups), name)
		for g, msgs := range groups {
			if g == "$schema" || slices.Contains(_runtimeGroups, g) {
				continue
			}
			m, ok := msgs.(map[string]any)
			require.True(t, ok, "%s: %s: not a group", name, g)
			for k := range m {
				if _, ok := keys[k]; !ok {
					unused = append(unused, name+": "+g+": "+strconv.Quote(k))
				}
			}
		}
	}
	slices.Sort(unused)
	assert.Empty(t, unused, "unused keys, delete them or move runtime ones to %v", _runtimeGroups)
}

// TestRuntimeKeys checks that ru.json translates the keys built at runtime: enum
// values, the titles of the operation form and month and weekday names.
func TestRuntimeKeys(t *testing.T) {
	t.Parallel()

	d, err := i18n.Load(web.FS)
	require.NoError(t, err)

	var keys []string
	add := func(vs ...any) {
		for _, v := range vs {
			keys = append(keys, fmt.Sprint(v))
		}
	}
	for _, v := range domain.OpTypes() {
		add(v, "New "+string(v), "Edit "+string(v))
	}
	for _, v := range domain.Kinds() {
		add(v)
	}
	for _, v := range domain.AccountKinds() {
		add(v)
	}
	for _, f := range domain.RuleFields() {
		add(f.Field)
		for _, op := range f.Ops {
			add(op)
		}
	}
	add(domain.ImportStaged, domain.ImportCommitted, domain.ImportUndone, domain.ScopeRead, domain.ScopeWrite)
	for m := time.January; m <= time.December; m++ {
		tm := time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC)
		add(tm.Format("January"), tm.Format("Jan"), tm.Format("Jan")+"|date")
	}
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		add(wd.String()[:3])
	}

	var missing []string
	for _, k := range keys {
		if _, ok := d["ru"][k]; !ok {
			missing = append(missing, strconv.Quote(k))
		}
	}
	slices.Sort(missing)
	assert.Empty(t, slices.Compact(missing), "untranslated runtime keys")
}

// TestLocaleOrder checks that the groups of the locale files and the keys of each group
// are sorted, so that a key has one place to go and diffs stay small.
func TestLocaleOrder(t *testing.T) {
	t.Parallel()

	for name, b := range localeFiles(t) {
		dec := json.NewDecoder(bytes.NewReader(b))
		var groups []string
		keys := map[string][]string{}
		// strings at depth 1 are groups, at depth 2 keys, whose values are decoded whole
		depth := 0
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, name)
			switch tok := tok.(type) {
			case json.Delim:
				if tok == '{' {
					depth++
				} else {
					depth--
				}
			case string:
				if depth == 1 && tok == "$schema" {
					var v string
					require.NoError(t, dec.Decode(&v), name)
					continue
				}
				if depth == 1 {
					groups = append(groups, tok)
					continue
				}
				g := groups[len(groups)-1]
				keys[g] = append(keys[g], tok)
				var v json.RawMessage
				require.NoError(t, dec.Decode(&v), name)
			}
		}
		assert.True(t, slices.IsSorted(groups), "%s: groups not sorted: %v", name, groups)
		for g, ks := range keys {
			assert.True(t, slices.IsSorted(ks), "%s: %s: keys not sorted", name, g)
		}
	}
}

// TestRussianPluralForms checks that every plural message of ru.json has each form
// that Russian picks.
func TestRussianPluralForms(t *testing.T) {
	t.Parallel()

	d, err := i18n.Load(web.FS)
	require.NoError(t, err)
	ru := language.Russian
	for k, m := range d["ru"] {
		if m.Forms == nil {
			continue
		}
		for n := range 200 {
			f := plural.Cardinal.MatchPlural(ru, n, 0, 0, 0, 0)
			if _, ok := m.Forms[f]; !ok {
				assert.Fail(t, "missing plural form", "%q: no form for %d", k, n)
				break
			}
		}
	}
}

// localeFiles returns the locale files of the UI languages that have one, by name.
func localeFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, l := range i18n.Languages() {
		name := "locales/" + l.Code + ".json"
		b, err := fs.ReadFile(web.FS, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		files[name] = b
	}
	require.NotEmpty(t, files)
	return files
}
