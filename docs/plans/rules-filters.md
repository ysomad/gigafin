# Rules page: search and filters

Goal: the rules list gets the same search box, Filter popover, filter count badge and
Reset link as the operations list (`internal/ui/operations/list.templ`), built from the
same Oat components, with no new CSS or JS.

## 1. What the operations page does (to mirror)

- A `GET /operations` form on the right of the toolbar:
  - `<fieldset class="group">` with `<input type="search" name="q">` and a Search
    button;
  - an `<ot-dropdown>` "Filter" button, with `<span class="badge">` showing
    `Filter.Count()` (all set fields but the query), opening
    `<article class="card p-4 form-compact" popover id="filter-popover">` with
    `kit.Field` selects in a `row`, and a Close / Apply footer;
  - a ghost "Reset" link when `Filter.Filtered()`.
- The primary button on the left is an `<ot-dropdown><menu class="buttons">` with a
  "▾" menu for secondary actions (Run rules, Rules).
- The handler parses the query into `domain.Filter` (`kit.FilterFromQuery`), passes it
  to the service, and renders `Filter`, `Filtered`, `FilterCount` in the view.
- When filtered and empty: "No operations yet." in `text-light`.

## 2. Filters for rules

| Query param | Filter field | Matches a rule that… |
|---|---|---|
| `q` | `Query` | has the text, ignoring case and ё/е (`foldText`), in a condition value, an action value, or the name of a category it refers to |
| `stage` | `Stage` | runs at that stage (pre / default / post) |
| `origin` | `Origin` | is the user's or learned |
| `field` | `Field` | has a condition on that field (payee, bank payee, amount, …) |
| `action` | `Action` | has an action of that type (set category, rename payee, …) |
| `category` | `CategoryID` | refers to the category in a condition or `set_category`; a root category also matches its children, as in the operations filter |
| `account` | `AccountID` | has an account condition with that account |

Not included: amount and date ranges (rules have none), tags (only `add_tag` values,
already found by `q`).

## 3. Domain (`internal/domain/rule.go`)

```go
// RuleFilter selects rules. Zero fields match everything.
type RuleFilter struct {
    Query      string
    Stage      RuleStage
    Origin     RuleOrigin
    Field      RuleField
    Action     ActionType
    // CategoryIDs are the category filtered by and its children, any of them matches.
    CategoryIDs []int64
    AccountID   int64
}

func (f RuleFilter) Filtered() bool
func (f RuleFilter) Count() int        // set fields but Query, for the badge
func (f RuleFilter) Validate() error    // stage, origin, field, action from their lists
// Match reports whether r passes f; names are category names by id, for Query.
func (f RuleFilter) Match(r Rule, names map[int64]string) bool
```

- `Match` reuses `foldText` and `Rule.UsesCategory` (one call per id in
  `CategoryIDs`).
- Table test in `rule_test.go`: one rule set, one case per field plus query on a value,
  on a category name, and with ё/case differences.

## 4. Service (`internal/service/rule.go`)

`Rules.List(ctx, userID, f domain.RuleFilter) ([]domain.Rule, error)`:

- validate `f`, read all rules (`Rules.List` repo, as now), and when `f.Filtered()`
  read categories once for names and keep the rules `f.Match` accepts;
- filter in Go, not SQL: rules are stored as JSON blobs, and a user has hundreds at
  most (257 now), so one read and an in-memory pass is simpler than JSON queries. The
  storage stays dumb.
- resolving a root `CategoryID` to itself plus children happens in the UI parser
  (it has the tree), so the domain gets plain ids.

Callers to update: `ui/rules.rules`, `api.ListRules` (pass `domain.RuleFilter{}`), and
`service/rule_test.go`.

## 5. UI (`internal/ui/rules`)

- `rules.go`: `ruleFilterFromQuery(q url.Values, tree []domain.Category)
  (domain.RuleFilter, error)` — local to the feature (only rules use it, so it stays
  out of `kit`); decoded with `h.app.Decoder` like `kit.FilterFromQuery`; an invalid
  value is `ErrInvalid` "invalid filter".
- `view.go`: `rulesData` gains `Filter domain.RuleFilter`, `Filtered bool`,
  `FilterCount int`, `Total int` (all rules, to show "N of M"), plus `Categories`,
  `Accounts` for the selects.
- `list.templ` toolbar, mirroring operations:
  - left: `<ot-dropdown><menu class="buttons">` with "+ New rule" and a "▾" menu
    holding "Learn from history" (a post form with `role="menuitem" class="ghost"`
    button, like "Run rules"), replacing today's separate button;
  - right: `GET /rules` form with the search group (placeholder "payee, note or
    category"), the Filter dropdown with the count badge, and Reset;
  - `filterPopover(d)`: `row` of `kit.Field` selects — Stage (col-6), Origin (col-6),
    Condition field (col-6), Action (col-6), Category (col-12, `kit.CategoryOptions`),
    Account (col-12) — each with an "All" option; footer Close / Apply.
- Results: keep the stage tables; above them, when filtered,
  `<p class="text-light">` "Showing %d of %d rules."; when nothing matches,
  "No rules match." in `text-light`.
- The `learned=N` notice and redirect stay as they are; Learn keeps the current query
  out of the redirect (it changes the rule set).
- Reuse the existing labels (`ruleStageLabel`, `ruleFieldLabel`, `ruleActionLabel`) for
  the options; new strings to `web/locales/ru.json`, sorted (`TestLocaleOrder`).
- No pagination for now: hundreds of rows render fine. If it grows past ~1000, add the
  operations `MoreURL` / `hx-trigger="revealed"` pattern.

## 6. API (optional, second step)

Turn `GET /rules` into a `query` (QUERY method, like `listOperations`) with a
`RuleQuery` schema (`query`, `stage`, `origin`, `field`, `action`, `category_id`,
`account_id`), `make generate`, update `api.ListRules`; the MCP tool follows from the
spec (update the tool count in `mcp_test.go` only if an operation is added).

## 7. Order of work

1. `domain.RuleFilter` + `Match` + table test.
2. `Rules.List` with the filter; update callers and service test.
3. UI parser, view data, toolbar, popover, empty/count text; locale strings.
4. Optional API query.
5. `make generate test lint`; check the page in the browser at desktop and phone width.

## 8. Open questions

- Should "Learn from history" move into the "▾" menu (matches operations) or stay a
  visible button?
- Should the search also match the condition operator and action labels ("contains",
  "rename payee"), or only values and category names? Plan: values and names only;
  operators and action types go through the popover.
