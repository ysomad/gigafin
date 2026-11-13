package rules

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the rules pages.
type Handler struct{ app *kit.App }

// New returns the handler of the rules pages.
func New(app *kit.App) *Handler { return &Handler{app: app} }

// Routes returns the routes of the rules pages.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /rules":              h.rules,
		"GET /rules/new":          h.newRule,
		"POST /rules":             h.createRule,
		"GET /rules/condition":    h.ruleCondition,
		"GET /rules/action":       h.ruleAction,
		"POST /rules/preview":     h.previewRule,
		"GET /rules/{id}":         h.rule,
		"POST /rules/{id}":        h.updateRule,
		"POST /rules/{id}/apply":  h.applyRule,
		"POST /rules/{id}/delete": h.deleteRule,
	}
}

// _ruleValueSep separates the values of one_of, not_one_of and between in a condition
// value input.
const _ruleValueSep = "|"

func (h *Handler) rules(w http.ResponseWriter, r *http.Request, user domain.User) error {
	rules, err := h.app.Svc.Rules.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	catNames, accByID := domain.CategoryNames(tree), domain.AccountsByID(accounts)
	// name returns the name of the category or account with the id in v, v otherwise
	name := func(field domain.RuleField, v string) string {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return v
		}
		if n, ok := catNames[id]; ok && field == domain.RuleFieldCategory {
			return n
		}
		if a, ok := accByID[id]; ok && field == domain.RuleFieldAccount {
			return a.Name
		}
		return v
	}
	var d rulesData
	for _, stage := range domain.RuleStages() {
		s := stageRules{Stage: stage}
		for _, rule := range rules {
			if rule.Stage != stage {
				continue
			}
			v := ruleView{
				ID: rule.ID, Origin: rule.Origin,
				Conds:   make([]cond, len(rule.Conditions)),
				Actions: make([]actionRow, len(rule.Actions)),
			}
			for i, c := range rule.Conditions {
				values := make([]string, len(c.Values))
				for j, val := range c.Values {
					values[j] = name(c.Field, val)
				}
				v.Conds[i] = cond{
					Field: c.Field, Op: c.Op, Values: strings.Join(values, " "+_ruleValueSep+" "),
					Any: rule.Match == domain.RuleMatchAny,
				}
			}
			for i, a := range rule.Actions {
				v.Actions[i] = actionRow{Type: a.Type, Value: a.Value}
				if a.Type == domain.ActionSetCategory {
					v.Actions[i].Value = name(domain.RuleFieldCategory, a.Value)
				}
			}
			s.Rules = append(s.Rules, v)
		}
		if len(s.Rules) > 0 {
			d.Stages = append(d.Stages, s)
		}
	}
	return kit.Render(h.app, w, r, http.StatusOK, listPage, kit.Page{User: &user, Nav: "settings", Title: "Rules"}, d,
		kit.Failure{})
}

// newCondRow returns a condition row of field, the first field when it is unknown, and
// op, the first operator of the field when the field does not take it.
func newCondRow(field domain.RuleField, op domain.CondOp, value string) condRow {
	ops, ok := domain.CondOps(field)
	if !ok {
		first := domain.RuleFields()[0]
		field, ops = first.Field, first.Ops
	}
	row := condRow{Field: field, Op: op, Value: value, Ops: ops}
	if !slices.Contains(row.Ops, op) {
		row.Op = row.Ops[0]
	}
	row.ID, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64) //nolint:errcheck // 0 is not an id
	return row
}

func (h *Handler) renderRule(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f ruleForm, viewErr error,
) error {
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	if f.Accounts, err = h.app.Svc.Accounts.List(r.Context(), user.ID); err != nil {
		return err
	}
	f.Categories, f.Fields = tree, domain.RuleFields()
	if len(f.Conditions) == 0 {
		f.Conditions = []condRow{newCondRow(domain.RuleFieldPayee, domain.CondIs, "")}
	}
	if len(f.Actions) == 0 {
		f.Actions = []actionRow{{Type: domain.ActionSetCategory}}
	}
	title := "New rule"
	if f.ID != 0 {
		title = "Rule"
	}
	return kit.Render(h.app, w, r, status, formPage, kit.Page{User: &user, Nav: "settings", Title: title}, f,
		kit.Failure{Err: viewErr, Form: "rule"})
}

// newRule renders an empty rule form, or one setting the category of the payee in the
// query, so an operation can start a rule.
func (h *Handler) newRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	f := ruleForm{Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll, Applied: -1}
	if payee := strings.TrimSpace(q.Get("payee")); payee != "" {
		f.Conditions = []condRow{newCondRow(domain.RuleFieldPayee, domain.CondIs, payee)}
	}
	if id, err := strconv.ParseInt(q.Get("category"), 10, 64); err == nil {
		f.Actions = []actionRow{{Type: domain.ActionSetCategory, Value: strconv.FormatInt(id, 10), ID: id}}
	}
	return h.renderRule(w, r, user, http.StatusOK, f, nil)
}

func (h *Handler) rule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	rule, err := h.app.Svc.Rules.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	f := ruleForm{ID: rule.ID, Origin: rule.Origin, Stage: rule.Stage, Match: rule.Match, Applied: -1}
	if n, err := strconv.Atoi(r.URL.Query().Get("applied")); err == nil && n >= 0 {
		f.Applied = n
	}
	f.Conditions = make([]condRow, len(rule.Conditions))
	for i, c := range rule.Conditions {
		f.Conditions[i] = newCondRow(c.Field, c.Op, strings.Join(c.Values, " "+_ruleValueSep+" "))
	}
	f.Actions = make([]actionRow, len(rule.Actions))
	for i, a := range rule.Actions {
		f.Actions[i] = actionRow{Type: a.Type, Value: a.Value}
		f.Actions[i].ID, _ = strconv.ParseInt(a.Value, 10, 64) //nolint:errcheck // 0 is not an id
	}
	return h.renderRule(w, r, user, http.StatusOK, f, nil)
}

// parseRuleForm reads the form into both the rows, for re-rendering, and the domain
// input, and returns the errors of its invalid inputs. Condition
// rows without a value are dropped; row messages are keyed by the index of the row
// among the ones kept.
//
//nolint:cyclop // every input of the form is checked here, so the checks stay together
func (h *Handler) parseRuleForm(r *http.Request) (ruleForm, domain.RuleInput, kit.FieldsError, error) {
	f := ruleForm{Applied: -1}
	// condition and action rows are added client-side, so they come as parallel lists
	var in struct {
		Stage      string   `form:"stage"`
		Match      string   `form:"match"`
		CondFields []string `form:"cond_field"`
		CondOps    []string `form:"cond_op"`
		CondValues []string `form:"cond_value"`
		ActTypes   []string `form:"act_type"`
		ActValues  []string `form:"act_value"`
	}
	if err := h.app.DecodeForm(r, &in); err != nil {
		return f, domain.RuleInput{}, nil, err
	}
	if len(in.CondOps) != len(in.CondFields) || len(in.CondValues) != len(in.CondFields) ||
		len(in.ActValues) != len(in.ActTypes) {
		return f, domain.RuleInput{}, nil, domain.NewError(errors.ErrInvalid, "rule rows do not match")
	}
	errs := kit.FieldsError{}
	f.Stage, f.Match = domain.RuleStage(in.Stage), domain.RuleMatch(in.Match)
	if !slices.Contains(domain.RuleStages(), f.Stage) {
		errs["stage"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	if f.Match != domain.RuleMatchAll && f.Match != domain.RuleMatchAny {
		errs["match"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	rule := domain.RuleInput{Stage: f.Stage, Match: f.Match}
	for i, field := range in.CondFields {
		if strings.TrimSpace(in.CondValues[i]) == "" {
			continue
		}
		c := domain.Condition{Field: domain.RuleField(field), Op: domain.CondOp(in.CondOps[i])}
		n := "[" + strconv.Itoa(len(f.Conditions)) + "]"
		f.Conditions = append(f.Conditions, newCondRow(c.Field, c.Op, in.CondValues[i]))
		want := 1
		switch c.Op {
		case domain.CondOneOf, domain.CondNotOneOf:
			c.Values, want = strings.Split(in.CondValues[i], _ruleValueSep), 0
		case domain.CondBetween:
			c.Values, want = strings.Split(in.CondValues[i], _ruleValueSep), 2
		default:
			c.Values = []string{in.CondValues[i]}
		}
		rule.Conditions = append(rule.Conditions, c)
		ops, ok := domain.CondOps(c.Field)
		if !ok {
			errs["cond_field"+n] = domain.NewError(errors.ErrInvalid, "choose from the list")
			continue
		}
		if !slices.Contains(ops, c.Op) {
			errs["cond_op"+n] = domain.NewError(errors.ErrInvalid, "choose from the list")
			continue
		}
		if want != 0 && len(c.Values) != want {
			errs["cond_value"+n] = domain.NewError(errors.ErrInvalid, "%s takes %d values", domain.Msg(c.Op), want)
			continue
		}
		for _, v := range c.Values {
			v = strings.TrimSpace(v)
			var err error
			switch {
			case v == "":
				errs["cond_value"+n] = domain.NewError(errors.ErrInvalid, "values must not be empty")
			case utf8.RuneCountInString(v) > domain.MaxTextLen:
				errs["cond_value"+n] = domain.NewError(errors.ErrInvalid, "too long")
			case c.Field == domain.RuleFieldAmount:
				if _, err = money.Parse(v); err != nil {
					errs["cond_value"+n] = domain.NewError(errors.ErrInvalid, "enter a number")
				}
			case c.Op == domain.CondMatches:
				if _, err = regexp.Compile("(?i)" + v); err != nil {
					errs["cond_value"+n] = domain.NewError(errors.ErrInvalid, "%q is not a regular expression", v)
				}
			}
		}
	}
	f.Actions = make([]actionRow, len(in.ActTypes))
	rule.Actions = make([]domain.Action, len(in.ActTypes))
	for i, typ := range in.ActTypes {
		a := domain.Action{Type: domain.ActionType(typ), Value: strings.TrimSpace(in.ActValues[i])}
		f.Actions[i] = actionRow{Type: a.Type, Value: in.ActValues[i]}
		f.Actions[i].ID, _ = strconv.ParseInt(a.Value, 10, 64) //nolint:errcheck // 0 is not an id
		rule.Actions[i] = a
		n := "[" + strconv.Itoa(i) + "]"
		switch {
		case !slices.Contains(domain.ActionTypes(), a.Type):
			errs["act_type"+n] = domain.NewError(errors.ErrInvalid, "choose from the list")
		case utf8.RuneCountInString(a.Value) > domain.MaxTextLen:
			errs["act_value"+n] = domain.NewError(errors.ErrInvalid, "too long")
		case a.Value == "" && a.Type != domain.ActionSetNote:
			errs["act_value"+n] = domain.NewError(errors.ErrInvalid, "required")
		}
	}
	return f, rule, errs, nil
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	f, in, errs, err := h.parseRuleForm(r)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return h.renderRule(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	_, err = h.app.Svc.Rules.Create(r.Context(), user.ID, in)
	if kit.IsUserError(err) {
		return h.renderRule(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/rules")
	return nil
}

func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	f, in, errs, err := h.parseRuleForm(r)
	if err != nil {
		return err
	}
	f.ID = id
	if len(errs) > 0 {
		return h.renderRule(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	_, err = h.app.Svc.Rules.Update(r.Context(), user.ID, id, in)
	if kit.IsUserError(err) {
		return h.renderRule(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/rules")
	return nil
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Rules.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	kit.Redirect(w, r, "/rules")
	return nil
}

func (h *Handler) applyRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	n, err := h.app.Svc.Rules.Apply(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/rules/"+strconv.FormatInt(id, 10)+"?applied="+strconv.Itoa(n))
	return nil
}

// previewRule renders the operations the rule in the form matches.
func (h *Handler) previewRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	const limit = 20
	var d rulePreview
	// invalid inputs are shown on saving; the preview shows what the service rejects
	_, in, _, err := h.parseRuleForm(r) //nolint:errcheck // input messages are not shown here
	if err != nil {
		d.Error = err
		return h.app.Fragment(w, r, http.StatusOK, previewList(d))
	}
	ops, total, err := h.app.Svc.Rules.Preview(r.Context(), user.ID, in, limit)
	if kit.IsUserError(err) {
		d.Error = err
		return h.app.Fragment(w, r, http.StatusOK, previewList(d))
	}
	if err != nil {
		return err
	}
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	d.Total, d.Days, d.CatNames = total, kit.GroupByDay(ops), domain.CategoryNames(tree)
	d.AccByID = domain.AccountsByID(accounts)
	return h.app.Fragment(w, r, http.StatusOK, previewList(d))
}

// ruleCondition renders a new condition row, or the row of the field in the query
// when its field changes.
func (h *Handler) ruleCondition(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	row := newCondRow(domain.RuleField(q.Get("cond_field")), domain.CondOp(q.Get("cond_op")), "")
	return h.app.Fragment(w, r, http.StatusOK, condFields(row, domain.RuleFields(), tree, accounts, 0, nil))
}

// ruleAction renders a new action row, or the row of the type in the query when its
// type changes.
func (h *Handler) ruleAction(w http.ResponseWriter, r *http.Request, user domain.User) error {
	tree, err := h.app.Svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	typ := domain.ActionType(r.URL.Query().Get("act_type"))
	if !slices.Contains(domain.ActionTypes(), typ) {
		typ = domain.ActionSetCategory
	}
	return h.app.Fragment(w, r, http.StatusOK, actionFields(actionRow{Type: typ}, tree, 0, nil))
}
