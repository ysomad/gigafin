package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

// _ruleValueSep separates the values of one_of, not_one_of and between in a condition
// value input.
const _ruleValueSep = "|"

type rulesData struct {
	// Stages are the rules of each stage that has any, oldest first, in the order
	// stages run.
	Stages   []ruleStage
	Settings domain.RuleSettings
}

type ruleStage struct {
	Stage domain.RuleStage
	Rules []ruleView
}

// ruleView is a rule with the ids in its values replaced by names.
type ruleView struct {
	ID      int64
	Origin  domain.RuleOrigin
	Conds   []condView
	Actions []actionRow
}

type condView struct {
	Field domain.RuleField
	Op    domain.CondOp
	// Values are the values joined by _ruleValueSep.
	Values string
	// Any joins the condition to the one before with "or".
	Any bool
}

func (u *UI) rules(w http.ResponseWriter, r *http.Request, user domain.User) error {
	rules, err := u.svc.Rules.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	settings, err := u.svc.Rules.Settings(r.Context(), user.ID)
	if err != nil {
		return err
	}
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	catNames := domain.CategoryNames(tree)
	accNames := make(map[int64]string, len(accounts))
	for _, a := range accounts {
		accNames[a.ID] = a.Name
	}
	// name returns the name of the category or account with the id in v, v otherwise
	name := func(names map[int64]string, v string) string {
		id, err := strconv.ParseInt(v, 10, 64)
		if n, ok := names[id]; err == nil && ok {
			return n
		}
		return v
	}
	d := rulesData{Settings: settings}
	for _, stage := range domain.RuleStages() {
		s := ruleStage{Stage: stage}
		for _, rule := range rules {
			if rule.Stage != stage {
				continue
			}
			v := ruleView{
				ID: rule.ID, Origin: rule.Origin,
				Conds:   make([]condView, len(rule.Conditions)),
				Actions: make([]actionRow, len(rule.Actions)),
			}
			for i, c := range rule.Conditions {
				values := make([]string, len(c.Values))
				for j, val := range c.Values {
					switch c.Field {
					case domain.RuleFieldCategory:
						values[j] = name(catNames, val)
					case domain.RuleFieldAccount:
						values[j] = name(accNames, val)
					default:
						values[j] = val
					}
				}
				v.Conds[i] = condView{
					Field: c.Field, Op: c.Op, Values: strings.Join(values, " "+_ruleValueSep+" "),
					Any: rule.Match == domain.RuleMatchAny,
				}
			}
			for i, a := range rule.Actions {
				v.Actions[i] = actionRow{Type: a.Type, Value: a.Value}
				if a.Type == domain.ActionSetCategory {
					v.Actions[i].Value = name(catNames, a.Value)
				}
			}
			s.Rules = append(s.Rules, v)
		}
		if len(s.Rules) > 0 {
			d.Stages = append(d.Stages, s)
		}
	}
	return u.render(w, r, http.StatusOK, "rules", view{User: &user, Nav: "settings", Title: "Rules", Data: d})
}

func (u *UI) setRuleLearning(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Rules.SetLearning(r.Context(), user.ID, r.PostFormValue("learning") == "on"); err != nil {
		return err
	}
	redirect(w, r, "/rules")
	return nil
}

// condRow is a condition row of the rule form, with the operators of its field.
type condRow struct {
	Field domain.RuleField
	Op    domain.CondOp
	// Value is the values joined by _ruleValueSep.
	Value string
	// ID is Value as a category or account id, 0 when it is not one.
	ID  int64
	Ops []domain.CondOp
}

type actionRow struct {
	Type  domain.ActionType
	Value string
	// ID is Value as a category id, 0 when it is not one.
	ID int64
}

// ruleForm is the rule form; untagged fields are view only.
type ruleForm struct {
	ID         int64
	Origin     domain.RuleOrigin
	Stage      domain.RuleStage
	Match      domain.RuleMatch
	Conditions []condRow
	Actions    []actionRow
	// Applied is the number of operations the rule just changed, -1 when not applied.
	Applied    int
	Fields     []domain.RuleFieldOps
	Categories []domain.Category
	Accounts   []domain.Account
}

// newCondRow returns a condition row of field, the first field when it is unknown, and
// op, the first operator of the field when the field does not take it.
func newCondRow(field domain.RuleField, op domain.CondOp, value string) condRow {
	fields := domain.RuleFields()
	i := max(slices.IndexFunc(fields, func(f domain.RuleFieldOps) bool { return f.Field == field }), 0)
	row := condRow{Field: fields[i].Field, Op: op, Value: value, Ops: fields[i].Ops}
	if !slices.Contains(row.Ops, op) {
		row.Op = row.Ops[0]
	}
	row.ID, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64) //nolint:errcheck // 0 is not an id
	return row
}

func (u *UI) renderRule(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f ruleForm, viewErr error,
) error {
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	if f.Accounts, err = u.svc.Accounts.List(r.Context(), user.ID); err != nil {
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
	return u.render(w, r, status, "rule", view{User: &user, Nav: "settings", Title: title, Error: viewErr, Data: f})
}

// newRule renders an empty rule form, or one setting the category of the payee in the
// query, so an operation can start a rule.
func (u *UI) newRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	f := ruleForm{Stage: domain.RuleStageDefault, Match: domain.RuleMatchAll, Applied: -1}
	if payee := strings.TrimSpace(q.Get("payee")); payee != "" {
		f.Conditions = []condRow{newCondRow(domain.RuleFieldPayee, domain.CondIs, payee)}
	}
	if id, err := strconv.ParseInt(q.Get("category"), 10, 64); err == nil {
		f.Actions = []actionRow{{Type: domain.ActionSetCategory, Value: strconv.FormatInt(id, 10), ID: id}}
	}
	return u.renderRule(w, r, user, http.StatusOK, f, nil)
}

func (u *UI) rule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	rule, err := u.svc.Rules.Get(r.Context(), user.ID, id)
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
	return u.renderRule(w, r, user, http.StatusOK, f, nil)
}

// parseRuleForm reads the form into both the rows, for re-rendering, and the domain
// input. Condition rows without a value are dropped.
func (u *UI) parseRuleForm(r *http.Request) (ruleForm, domain.RuleInput, error) {
	f := ruleForm{Applied: -1}
	if err := r.ParseForm(); err != nil {
		return f, domain.RuleInput{}, fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
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
	if err := u.formDecoder.Decode(&in, r.PostForm); err != nil {
		return f, domain.RuleInput{}, fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if len(in.CondOps) != len(in.CondFields) || len(in.CondValues) != len(in.CondFields) ||
		len(in.ActValues) != len(in.ActTypes) {
		return f, domain.RuleInput{}, domain.NewError(errors.ErrInvalid, "form: rule rows do not match")
	}
	f.Stage, f.Match = domain.RuleStage(in.Stage), domain.RuleMatch(in.Match)
	rule := domain.RuleInput{Stage: f.Stage, Match: f.Match}
	for i, field := range in.CondFields {
		if strings.TrimSpace(in.CondValues[i]) == "" {
			continue
		}
		c := domain.Condition{Field: domain.RuleField(field), Op: domain.CondOp(in.CondOps[i])}
		f.Conditions = append(f.Conditions, newCondRow(c.Field, c.Op, in.CondValues[i]))
		switch c.Op {
		case domain.CondOneOf, domain.CondNotOneOf, domain.CondBetween:
			c.Values = strings.Split(in.CondValues[i], _ruleValueSep)
		default:
			c.Values = []string{in.CondValues[i]}
		}
		rule.Conditions = append(rule.Conditions, c)
	}
	f.Actions = make([]actionRow, len(in.ActTypes))
	rule.Actions = make([]domain.Action, len(in.ActTypes))
	for i, typ := range in.ActTypes {
		f.Actions[i] = actionRow{Type: domain.ActionType(typ), Value: in.ActValues[i]}
		f.Actions[i].ID, _ = strconv.ParseInt(in.ActValues[i], 10, 64) //nolint:errcheck // 0 is not an id
		rule.Actions[i] = domain.Action{Type: domain.ActionType(typ), Value: in.ActValues[i]}
	}
	return f, rule, nil
}

func (u *UI) createRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	f, in, err := u.parseRuleForm(r)
	if err == nil {
		_, err = u.svc.Rules.Create(r.Context(), user.ID, in)
	}
	if isUserError(err) {
		return u.renderRule(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/rules")
	return nil
}

func (u *UI) updateRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	f, in, err := u.parseRuleForm(r)
	f.ID = id
	if err == nil {
		_, err = u.svc.Rules.Update(r.Context(), user.ID, id, in)
	}
	if isUserError(err) {
		return u.renderRule(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/rules")
	return nil
}

func (u *UI) deleteRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Rules.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/rules")
	return nil
}

func (u *UI) applyRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	n, err := u.svc.Rules.Apply(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	redirect(w, r, "/rules/"+strconv.FormatInt(id, 10)+"?applied="+strconv.Itoa(n))
	return nil
}

type rulePreview struct {
	// Total is how many operations the rule matches; Days hold the latest of them.
	Total    int
	Error    error
	Days     []dayGroup
	CatNames map[int64]string
	AccByID  map[int64]domain.Account
	// MoreURL is always empty: op-rows reads it, but the preview has no more pages.
	MoreURL string
}

// previewRule renders the operations the rule in the form matches.
func (u *UI) previewRule(w http.ResponseWriter, r *http.Request, user domain.User) error {
	const limit = 20
	var d rulePreview
	_, in, err := u.parseRuleForm(r)
	if err != nil {
		d.Error = err
		return u.fragment(w, r, "rule-preview", d)
	}
	ops, total, err := u.svc.Rules.Preview(r.Context(), user.ID, in, limit)
	if isUserError(err) {
		d.Error = err
		return u.fragment(w, r, "rule-preview", d)
	}
	if err != nil {
		return err
	}
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	d.Total, d.Days, d.CatNames = total, groupByDay(ops), domain.CategoryNames(tree)
	d.AccByID = make(map[int64]domain.Account, len(accounts))
	for _, a := range accounts {
		d.AccByID[a.ID] = a
	}
	return u.fragment(w, r, "rule-preview", d)
}

// ruleCondition renders a new condition row, or the row of the field in the query
// when its field changes.
func (u *UI) ruleCondition(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	row := newCondRow(domain.RuleField(q.Get("cond_field")), domain.CondOp(q.Get("cond_op")), "")
	return u.fragment(w, r, "rule-condition", map[string]any{
		"Row": row, "Fields": domain.RuleFields(), "Categories": tree, "Accounts": accounts,
	})
}

// ruleAction renders a new action row, or the row of the type in the query when its
// type changes.
func (u *UI) ruleAction(w http.ResponseWriter, r *http.Request, user domain.User) error {
	tree, err := u.svc.Categories.Tree(r.Context(), user.ID)
	if err != nil {
		return err
	}
	typ := domain.ActionType(r.URL.Query().Get("act_type"))
	if !slices.Contains(domain.ActionTypes(), typ) {
		typ = domain.ActionSetCategory
	}
	return u.fragment(w, r, "rule-action", map[string]any{"Row": actionRow{Type: typ}, "Categories": tree})
}

// categorizeOperations runs rules on the operations matching the filter in the query
// and returns to them with the number changed.
func (u *UI) categorizeOperations(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	f, err := u.filterFromQuery(q)
	if err != nil {
		return err
	}
	n, err := u.svc.Operations.Categorize(r.Context(), user.ID, f)
	if err != nil {
		return err
	}
	q.Del("cursor")
	q.Set("changed", strconv.Itoa(n))
	redirect(w, r, "/operations?"+q.Encode())
	return nil
}

// fillOperation re-renders the new operation form with the empty fields rules fill.
func (u *UI) fillOperation(w http.ResponseWriter, r *http.Request, user domain.User) error {
	f, in, err := u.parseOpForm(r)
	if err != nil && !errors.Is(err, errors.ErrInvalid) {
		return err
	}
	filled, err := u.svc.Operations.Fill(r.Context(), user.ID, in)
	if err != nil {
		return err
	}
	f.Payee, f.Note, f.CategoryID = filled.Payee, filled.Note, filled.CategoryID
	var tags []string
	for _, t := range filled.Tags {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	f.Tags = strings.Join(tags, ", ")
	return u.renderForm(w, r, user, http.StatusOK, f, nil)
}
