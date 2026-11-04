package i18n_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/web"
)

func TestT(t *testing.T) {
	t.Parallel()

	d := i18n.Dicts{"ru": {"Save": "Сохранить", "Rate, %s": "Курс, %s"}}
	assert.Equal(t, "Сохранить", d.T("ru", "Save"))
	assert.Equal(t, "Save", d.T("en", "Save"))
	assert.Equal(t, "Missing", d.T("ru", "Missing"))
	assert.Equal(t, "Курс, USD", d.T("ru", "Rate, %s", "USD"))
	assert.Equal(t, "Rate, USD", d.T("en", "Rate, %s", "USD"))
	assert.Equal(t, "May", d.T("en", "May|date"))
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
	re := regexp.MustCompile(`[{(]-?\s*t\s+("(?:[^"\\]|\\.)*")`)
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
