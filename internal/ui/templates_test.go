package ui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// This suite renders the real templates without services, a server, or a database.
func TestTemplatesWithoutDatabase(t *testing.T) {
	t.Parallel()

	u, err := New(Config{SessionTTL: time.Hour, MaxUploadSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	amount := money.MustParse("125.25")
	category := domain.Category{ID: 1, Name: "Food", Kind: "expense"}
	account := domain.Account{ID: 1, Name: "Bank", Currency: "RUB", Kind: "credit", Balance: amount}
	plan := domain.PlanRow{
		Category: category, Currency: "RUB", Plan: money.MustParse("100"),
		Spent: amount, HasPlan: true, Repeating: true,
	}
	preview := domain.Preview{
		Import:     domain.Import{ID: 1, Status: "staged"},
		Accounts:   []domain.AccountNeed{{Name: "Bank", Currency: "RUB", Kind: "credit"}},
		Categories: []domain.CategoryNeed{{Name: "Food", Kind: "expense"}},
		Rows:       []domain.ImportRow{{Row: domain.Record{Currency: "RUB", Amount: amount}}},
		Skipped:    []domain.ImportRow{{Row: domain.Record{Currency: "RUB", Amount: amount}}},
	}
	// These elements require explicit end tags in our templates. Check their
	// nesting before a browser's HTML parser can silently repair broken branches.
	tags := regexp.MustCompile(`</?(?:div|span|form|article|table|thead|tbody|tr|td|th|label|fieldset|` +
		`ul|ol|li|details|summary|h[1-6])\b[^>]*>`)
	cases := []struct {
		name string
		page string
		data any
	}{
		{name: "login", page: "login", data: map[string]any{"Login": "demo"}},
		{name: "dashboard-empty", page: "dashboard", data: dashboardData{}},
		{name: "dashboard", page: "dashboard", data: dashboardData{
			Dash: domain.Dashboard{
				Totals: []domain.Total{{Currency: "RUB", Assets: amount}},
				Trends: []domain.Trend{{Currency: "RUB", Months: []domain.MonthTotal{{Expense: amount}}}},
				Expenses: []domain.CategoryShare{
					{Category: category, Currency: "RUB", Spent: amount, Prev: amount},
					{Category: category, Currency: "USD", Spent: amount},
				},
				TrendMonths: []timex.Month{{}},
				CategoryTrends: []domain.CategoryTrend{
					{Category: category, Currency: "RUB", Months: []money.Amount{amount}, Total: amount},
				},
				Payees: []domain.PayeeSum{{Payee: "Shop", Currency: "RUB", Total: amount}},
			},
			Charts: []trendChart{{Currency: "RUB", Months: []int64{0}, Income: []float64{1}, Expense: []float64{2}}},
		}},
		{name: "operations", page: "operations", data: opsData{
			Days: []dayGroup{{Ops: []domain.Operation{
				{ID: 1, Type: "expense", Currency: "RUB", Amount: amount, Tags: []string{"food"}},
				{ID: 2, Type: "income", Currency: "RUB", Amount: amount},
			}}},
			MoreURL: "/operations?cursor=next",
		}},
		{name: "operation-new", page: "operation_form", data: opForm{Type: "transfer"}},
		{name: "operation-split", page: "operation_split", data: opForm{
			ID: 1, Type: "expense", Parts: []splitForm{{Amount: "1"}},
		}},
		{name: "accounts", page: "accounts", data: accountsData{
			Assets: []domain.Account{account}, Liabilities: []domain.Account{account},
		}},
		{name: "account", page: "account", data: accountData{Account: account}},
		{name: "categories", page: "categories", data: categoriesData{
			Kind: "expense", Categories: []domain.Category{category},
		}},
		{name: "category", page: "category", data: categoryData{Category: category}},
		{name: "plans", page: "plans", data: plansData{
			Kind: "expense", Day: 15, Days: 30, Pace: 50,
			Totals: []planItem{{Row: plan}}, Planned: []planItem{{Row: plan, Includes: []domain.PlanRow{plan}}},
			Unplanned: []planItem{{Row: plan}},
		}},
		{name: "imports", page: "imports", data: importsData{Imports: []domain.Import{preview.Import}}},
		{name: "import-accounts", page: "import", data: importData{Step: "accounts", Preview: preview}},
		{name: "import-categories", page: "import", data: importData{Step: "categories", Preview: preview}},
		{name: "import-preview", page: "import", data: importData{Step: "preview", Preview: preview}},
		{name: "import-source", page: "import_new", data: importNewData{Step: "type"}},
		{name: "import-upload", page: "import_new", data: importNewData{Step: "file", Source: "gigafin"}},
		{name: "rates-upload", page: "import_new", data: importNewData{Step: "file", Rates: true}},
		{name: "rates-preview", page: "import_rates", data: importRatesData{}},
		{name: "rates-done", page: "import_rates", data: importRatesData{Done: true}},
		{name: "settings", page: "settings", data: settingsData{
			CanBackup: true, NewToken: "test-token", Settings: domain.Settings{RateSource: "cbr"},
			RateCurrencies: []string{"USD"}, Rates: []domain.Rate{{Currency: "USD"}},
			Sessions: []domain.Session{{}}, Keys: []domain.APIKey{{}},
		}},
		{name: "rules", page: "rules", data: rulesData{}},
		{name: "rule", page: "rule", data: ruleForm{
			ID: 1, Conditions: []condRow{{Field: "payee"}}, Actions: []actionRow{{Type: "set_note"}},
		}},
		{name: "notfound", page: "notfound"},
	}
	for _, tc := range cases {
		for _, lang := range []string{"en", "ru"} {
			t.Run(tc.name+"/"+lang, func(t *testing.T) {
				t.Parallel()

				tpl, cloneErr := u.pages[tc.page].Clone()
				if cloneErr != nil {
					t.Fatal(cloneErr)
				}
				tpl.Funcs(langFuncs(u.dicts, lang))
				var out bytes.Buffer
				if renderErr := tpl.ExecuteTemplate(&out, "layout", view{
					User: &domain.User{Login: "demo"}, Data: tc.data,
				}); renderErr != nil {
					t.Fatal(renderErr)
				}
				html := out.String()
				var stack []string
				for _, tag := range tags.FindAllString(html, -1) {
					name := strings.Fields(strings.Trim(tag, "</>"))[0]
					if !strings.HasPrefix(tag, "</") {
						stack = append(stack, name)
						continue
					}
					if len(stack) == 0 || stack[len(stack)-1] != name {
						t.Fatalf("unexpected closing tag %s, open elements: %v", tag, stack)
					}
					stack = stack[:len(stack)-1]
				}
				if len(stack) != 0 {
					t.Fatalf("unclosed elements: %v", stack)
				}
				for _, unwanted := range []string{"ZgotmplZ", "<no value>", "style=", "%!"} {
					if strings.Contains(html, unwanted) {
						t.Errorf("rendered output contains %q", unwanted)
					}
				}
				if tc.page == "plans" && (!strings.Contains(html, `value="100" max="100"`) ||
					!strings.Contains(html, "125%")) {
					t.Error("over-plan progress must clamp the graphic and retain the actual percentage")
				}
			})
		}
	}
}
