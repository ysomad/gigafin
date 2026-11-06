package domain

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// RuleStage orders rules: all pre rules run before default ones, post rules last.
// Pre rules usually clean up payees, so the rules after them match the clean name.
type RuleStage string

const (
	RuleStagePre     RuleStage = "pre"
	RuleStageDefault RuleStage = "default"
	RuleStagePost    RuleStage = "post"
)

// RuleStages returns all stages in the order they run.
func RuleStages() []RuleStage {
	return []RuleStage{RuleStagePre, RuleStageDefault, RuleStagePost}
}

// RuleMatch tells whether all conditions of a rule must hold or any of them.
type RuleMatch string

const (
	RuleMatchAll RuleMatch = "all"
	RuleMatchAny RuleMatch = "any"
)

// RuleOrigin is who made a rule.
type RuleOrigin string

const (
	// RuleOriginUser is a rule the user made or edited.
	RuleOriginUser RuleOrigin = "user"
	// RuleOriginLearned is a rule LearnCategory made from the user's categorization.
	RuleOriginLearned RuleOrigin = "learned"
)

// RuleSettings are a user's preferences for rules.
type RuleSettings struct {
	// Learning makes LearnCategory create rules when the user sets the category of an
	// operation by hand.
	Learning bool
}

// DefaultRuleSettings returns the settings of a user who never changed them.
func DefaultRuleSettings() RuleSettings {
	return RuleSettings{Learning: true}
}

// RuleField is the operation field a condition looks at.
type RuleField string

const (
	// RuleFieldSourcePayee is the payee as the bank sent it, see Operation.SourcePayee.
	RuleFieldSourcePayee RuleField = "source_payee"
	RuleFieldPayee       RuleField = "payee"
	RuleFieldNote        RuleField = "note"
	RuleFieldMCC         RuleField = "mcc"
	RuleFieldAmount      RuleField = "amount"
	RuleFieldAccount     RuleField = "account"
	RuleFieldCategory    RuleField = "category"
	RuleFieldType        RuleField = "type"
)

// CondOp compares a field to the values of a condition.
type CondOp string

const (
	CondIs          CondOp = "is"
	CondIsNot       CondOp = "is_not"
	CondContains    CondOp = "contains"
	CondNotContains CondOp = "not_contains"
	CondOneOf       CondOp = "one_of"
	CondNotOneOf    CondOp = "not_one_of"
	// CondMatches is a regular expression in RE2 syntax, matched ignoring case.
	CondMatches CondOp = "matches"
	// CondApprox is an amount within 7.5% of the value.
	CondApprox CondOp = "approx"
	// CondBetween is an amount between two values, both included.
	CondBetween CondOp = "between"
	CondGt      CondOp = "gt"
	CondGte     CondOp = "gte"
	CondLt      CondOp = "lt"
	CondLte     CondOp = "lte"
)

// RuleFields returns all condition fields with the operators each of them takes.
func RuleFields() []RuleFieldOps {
	text := []CondOp{CondIs, CondIsNot, CondContains, CondNotContains, CondOneOf, CondNotOneOf, CondMatches}
	ids := []CondOp{CondIs, CondIsNot, CondOneOf, CondNotOneOf}
	return []RuleFieldOps{
		{Field: RuleFieldSourcePayee, Ops: text},
		{Field: RuleFieldPayee, Ops: text},
		{Field: RuleFieldNote, Ops: text},
		{Field: RuleFieldMCC, Ops: text},
		{Field: RuleFieldAmount, Ops: []CondOp{CondIs, CondApprox, CondBetween, CondGt, CondGte, CondLt, CondLte}},
		{Field: RuleFieldAccount, Ops: ids},
		{Field: RuleFieldCategory, Ops: ids},
		{Field: RuleFieldType, Ops: ids},
	}
}

// RuleFieldOps is a condition field and the operators it takes.
type RuleFieldOps struct {
	Field RuleField
	Ops   []CondOp
}

// ActionType is what an action does to a matching operation.
type ActionType string

const (
	// ActionSetCategory sets the category of an expense or income to the category
	// with the id in the value, when the kinds match; others are left alone.
	ActionSetCategory ActionType = "set_category"
	ActionSetPayee    ActionType = "set_payee"
	// ActionSetNote replaces the note; an empty value clears it.
	ActionSetNote     ActionType = "set_note"
	ActionAppendNote  ActionType = "append_note"
	ActionPrependNote ActionType = "prepend_note"
	ActionAddTag      ActionType = "add_tag"
)

// ActionTypes returns all action types.
func ActionTypes() []ActionType {
	return []ActionType{
		ActionSetCategory, ActionSetPayee, ActionSetNote, ActionAppendNote, ActionPrependNote, ActionAddTag,
	}
}

// Condition is one test of an operation field. Values holds one value, except for
// one_of and not_one_of, which take one or more, and between, which takes two. Ids
// and amounts are decimal strings.
type Condition struct {
	Field  RuleField `json:"field"`
	Op     CondOp    `json:"op"`
	Values []string  `json:"values"`
}

// Action is one change to a matching operation.
type Action struct {
	Type  ActionType `json:"type"`
	Value string     `json:"value"`
}

// Rule changes operations that match its conditions, see RuleSet.
type Rule struct {
	ID         int64
	Stage      RuleStage
	Match      RuleMatch
	Conditions []Condition
	Actions    []Action
	Origin     RuleOrigin
	CreatedAt  time.Time
	// UpdatedAt is zero until the rule is first changed.
	UpdatedAt time.Time
}

// RuleInput creates or replaces a rule.
type RuleInput struct {
	Stage      RuleStage
	Match      RuleMatch
	Conditions []Condition
	Actions    []Action
}

// UsesCategory reports whether a condition or an action of the rule refers to the
// category with id.
func (r Rule) UsesCategory(id int64) bool {
	s := strconv.FormatInt(id, 10)
	for _, c := range r.Conditions {
		if c.Field == RuleFieldCategory && slices.Contains(c.Values, s) {
			return true
		}
	}
	for _, a := range r.Actions {
		if a.Type == ActionSetCategory && a.Value == s {
			return true
		}
	}
	return false
}

// NewRule validates in against the user's categories by id and returns a rule of
// origin. Values are trimmed. Errors are of kind errors.ErrInvalid.
func NewRule(in RuleInput, origin RuleOrigin, cats map[int64]Category) (Rule, error) {
	const (
		maxConditions = 20
		maxActions    = 20
	)
	r := Rule{Stage: in.Stage, Match: in.Match, Origin: origin}
	if !slices.Contains(RuleStages(), r.Stage) {
		return Rule{}, NewFieldError(errors.ErrInvalid, "stage", "must be pre, default or post")
	}
	if r.Match != RuleMatchAll && r.Match != RuleMatchAny {
		return Rule{}, NewFieldError(errors.ErrInvalid, "match", "must be all or any")
	}
	if len(in.Conditions) == 0 {
		return Rule{}, NewFieldError(errors.ErrInvalid, "conditions", "at least 1 condition")
	}
	if len(in.Conditions) > maxConditions {
		return Rule{}, NewFieldError(errors.ErrInvalid, "conditions", "at most %d conditions", maxConditions)
	}
	if len(in.Actions) == 0 {
		return Rule{}, NewFieldError(errors.ErrInvalid, "actions", "at least 1 action")
	}
	if len(in.Actions) > maxActions {
		return Rule{}, NewFieldError(errors.ErrInvalid, "actions", "at most %d actions", maxActions)
	}
	r.Conditions = make([]Condition, len(in.Conditions))
	for i, c := range in.Conditions {
		c.Values = slices.Clone(c.Values)
		for j, v := range c.Values {
			c.Values[j] = strings.TrimSpace(v)
		}
		if _, err := compileCondition(c, cats); err != nil {
			if e, ok := errors.AsType[Error](err); ok && e.Field != "" {
				e.Field = "conditions[" + strconv.Itoa(i) + "]." + e.Field
				return Rule{}, e
			}
			return Rule{}, NewError(err, "condition %d", i+1)
		}
		r.Conditions[i] = c
	}
	r.Actions = make([]Action, len(in.Actions))
	for i, a := range in.Actions {
		a.Value = strings.TrimSpace(a.Value)
		checked, err := checkAction(a, cats)
		if err != nil {
			if e, ok := errors.AsType[Error](err); ok && e.Field != "" {
				e.Field = "actions[" + strconv.Itoa(i) + "]." + e.Field
				return Rule{}, e
			}
			return Rule{}, NewError(err, "action %d", i+1)
		}
		r.Actions[i] = checked
	}
	return r, nil
}

// checkAction validates a and returns it with a tag value cleaned as operation tags are.
func checkAction(a Action, cats map[int64]Category) (Action, error) {
	if utf8.RuneCountInString(a.Value) > _maxTextLen {
		return Action{}, NewFieldError(errors.ErrInvalid, "value", "too long")
	}
	switch a.Type {
	case ActionSetCategory:
		id, err := strconv.ParseInt(a.Value, 10, 64)
		if err != nil {
			return Action{}, NewFieldError(errors.ErrInvalid, "value", "must be a category id")
		}
		if _, ok := cats[id]; !ok {
			return Action{}, NewFieldError(errors.ErrInvalid, "value", "category not found")
		}
	case ActionSetPayee, ActionAppendNote, ActionPrependNote:
		if a.Value == "" {
			return Action{}, NewFieldError(errors.ErrInvalid, "value", "required")
		}
	case ActionSetNote:
	case ActionAddTag:
		tags, err := newTags([]string{a.Value}, nil)
		if err != nil {
			return Action{}, err
		}
		if len(tags) == 0 {
			return Action{}, NewFieldError(errors.ErrInvalid, "value", "required")
		}
		a.Value = tags[0]
	default:
		return Action{}, NewFieldError(errors.ErrInvalid, "type", "unknown action %q", a.Type)
	}
	return a, nil
}

// condition is a Condition with its values parsed for the field.
type condition struct {
	Condition
	// text holds folded values of text fields, see foldText.
	text    []string
	ids     []int64
	amounts []money.Amount
	re      *regexp.Regexp
}

func compileCondition(c Condition, cats map[int64]Category) (condition, error) {
	const maxValues = 100
	i := slices.IndexFunc(RuleFields(), func(f RuleFieldOps) bool { return f.Field == c.Field })
	if i < 0 {
		return condition{}, NewFieldError(errors.ErrInvalid, "field", "unknown field %q", c.Field)
	}
	if !slices.Contains(RuleFields()[i].Ops, c.Op) {
		return condition{}, NewFieldError(errors.ErrInvalid, "op", "%s does not take %s", Msg(c.Field), Msg(c.Op))
	}
	want := 1
	switch c.Op {
	case CondOneOf, CondNotOneOf:
		want = 0
	case CondBetween:
		want = 2
	default:
	}
	switch {
	case want == 0 && len(c.Values) == 0:
		return condition{}, NewFieldError(errors.ErrInvalid, "values", "at least 1 value")
	case want == 0 && len(c.Values) > maxValues:
		return condition{}, NewFieldError(errors.ErrInvalid, "values", "at most %d values", maxValues)
	case want != 0 && len(c.Values) != want:
		return condition{}, NewFieldError(errors.ErrInvalid, "values", "%s takes %d values", Msg(c.Op), want)
	}
	cc := condition{Condition: c}
	for _, v := range c.Values {
		if err := cc.add(v, cats); err != nil {
			return condition{}, err
		}
	}
	if c.Op == CondBetween && cc.amounts[0].Cmp(cc.amounts[1]) > 0 {
		return condition{}, NewFieldError(errors.ErrInvalid, "values", "the first amount must not be above the second")
	}
	return cc, nil
}

// add parses the value v for the field of the condition.
func (c *condition) add(v string, cats map[int64]Category) error {
	if v == "" {
		return NewFieldError(errors.ErrInvalid, "values", "must not be empty")
	}
	if utf8.RuneCountInString(v) > _maxTextLen {
		return NewFieldError(errors.ErrInvalid, "values", "too long")
	}
	switch c.Field {
	case RuleFieldAmount:
		a, err := money.Parse(v)
		if err != nil {
			return NewFieldError(err, "values", "%q", v)
		}
		c.amounts = append(c.amounts, a)
	case RuleFieldAccount, RuleFieldCategory:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return NewFieldError(errors.ErrInvalid, "values", "%q is not an id", v)
		}
		if _, ok := cats[id]; c.Field == RuleFieldCategory && !ok {
			return NewFieldError(errors.ErrInvalid, "values", "category not found")
		}
		c.ids = append(c.ids, id)
	case RuleFieldType:
		if _, err := ParseOpType(v); err != nil {
			return err
		}
		c.text = append(c.text, v)
	default:
		if c.Op == CondMatches {
			re, err := regexp.Compile("(?i)" + v)
			if err != nil {
				return NewFieldError(errors.ErrInvalid, "values", "%q is not a regular expression", v)
			}
			c.re = re
		}
		c.text = append(c.text, foldText(v))
	}
	return nil
}

// score ranks how specific a condition is, as Actual Budget does: exact tests score
// highest, so rules with them run later and win.
func (c condition) score() int {
	switch c.Op {
	case CondIs, CondIsNot:
		return 10
	case CondOneOf, CondNotOneOf:
		return 9
	case CondApprox, CondBetween:
		return 5
	case CondGt, CondGte, CondLt, CondLte:
		return 1
	default:
		return 0
	}
}

func (c condition) match(in OperationInput) bool {
	switch c.Field {
	case RuleFieldAmount:
		return c.matchAmount(in.Amount)
	case RuleFieldAccount:
		return c.matchID(in.AccountID)
	case RuleFieldCategory:
		// an operation without a category, such as a transfer, is not "not X" either
		return in.CategoryID != 0 && c.matchID(in.CategoryID)
	case RuleFieldType:
		return c.matchText(string(in.Type), string(in.Type))
	case RuleFieldSourcePayee:
		return c.matchText(in.SourcePayee, foldText(in.SourcePayee))
	case RuleFieldPayee:
		return c.matchText(in.Payee, foldText(in.Payee))
	case RuleFieldNote:
		return c.matchText(in.Note, foldText(in.Note))
	case RuleFieldMCC:
		return c.matchText(in.MCC, foldText(in.MCC))
	}
	return false
}

// matchText tests a text field, raw for regular expressions and folded otherwise.
func (c condition) matchText(raw, folded string) bool {
	switch c.Op {
	case CondIs, CondOneOf:
		return slices.Contains(c.text, folded)
	case CondIsNot, CondNotOneOf:
		return !slices.Contains(c.text, folded)
	case CondContains:
		return strings.Contains(folded, c.text[0])
	case CondNotContains:
		return !strings.Contains(folded, c.text[0])
	case CondMatches:
		return c.re.MatchString(raw)
	default:
		return false
	}
}

func (c condition) matchID(id int64) bool {
	switch c.Op {
	case CondIs, CondOneOf:
		return slices.Contains(c.ids, id)
	case CondIsNot, CondNotOneOf:
		return !slices.Contains(c.ids, id)
	default:
		return false
	}
}

func (c condition) matchAmount(a money.Amount) bool {
	v := c.amounts[0]
	switch c.Op {
	case CondIs:
		return a.Equal(v)
	case CondApprox:
		// 7.5% is 3/40
		part := v.Abs().Div(40)
		return a.Sub(v).Abs().Cmp(part.Add(part).Add(part)) <= 0
	case CondBetween:
		return a.Cmp(v) >= 0 && a.Cmp(c.amounts[1]) <= 0
	case CondGt:
		return a.Cmp(v) > 0
	case CondGte:
		return a.Cmp(v) >= 0
	case CondLt:
		return a.Cmp(v) < 0
	case CondLte:
		return a.Cmp(v) <= 0
	default:
		return false
	}
}

// _textFolder folds ё to е after lowercasing, as the full-text search does.
var _textFolder = strings.NewReplacer("ё", "е")

// foldText returns s lowercased in any script, with ё folded to е, for comparing text
// ignoring case.
func foldText(s string) string {
	return _textFolder.Replace(strings.ToLower(s))
}

// RuleSet applies rules to operations in the order Actual Budget does: by stage, and
// within a stage from the least specific rule to the most specific one, so the most
// specific rule has the last word.
type RuleSet struct {
	rules []compiledRule
	cats  map[int64]Category
}

type compiledRule struct {
	rule  Rule
	conds []condition
	score int
}

// NewRuleSet compiles the user's rules against their categories by id.
func NewRuleSet(rules []Rule, cats map[int64]Category) (RuleSet, error) {
	rs := RuleSet{cats: cats, rules: make([]compiledRule, len(rules))}
	for i, r := range rules {
		cr := compiledRule{rule: r, conds: make([]condition, len(r.Conditions))}
		exact := true
		for j, c := range r.Conditions {
			cc, err := compileCondition(c, cats)
			if err != nil {
				return RuleSet{}, NewError(err, "rule %d: condition %d", r.ID, j+1)
			}
			cr.conds[j] = cc
			cr.score += cc.score()
			exact = exact && slices.Contains([]CondOp{CondIs, CondIsNot, CondApprox, CondOneOf, CondNotOneOf}, c.Op)
		}
		if exact {
			cr.score *= 2
		}
		rs.rules[i] = cr
	}
	slices.SortStableFunc(rs.rules, func(a, b compiledRule) int {
		return cmp.Or(
			cmp.Compare(slices.Index(RuleStages(), a.rule.Stage), slices.Index(RuleStages(), b.rule.Stage)),
			cmp.Compare(a.score, b.score),
			cmp.Compare(a.rule.ID, b.rule.ID),
		)
	})
	return rs, nil
}

// Len returns the number of rules in the set.
func (rs RuleSet) Len() int {
	return len(rs.rules)
}

// Apply runs every rule that matches in, each one seeing the changes of the rules
// before it, and reports whether any of them changed in.
func (rs RuleSet) Apply(in OperationInput) (OperationInput, bool) {
	in.Tags = slices.Clone(in.Tags)
	changed := false
	for _, r := range rs.rules {
		if !r.match(in) {
			continue
		}
		for _, a := range r.rule.Actions {
			changed = rs.apply(&in, a) || changed
		}
	}
	return in, changed
}

// Matches reports whether the rule with id is in the set and matches in.
func (rs RuleSet) Matches(id int64, in OperationInput) bool {
	i := slices.IndexFunc(rs.rules, func(r compiledRule) bool { return r.rule.ID == id })
	return i >= 0 && rs.rules[i].match(in)
}

func (r compiledRule) match(in OperationInput) bool {
	if r.rule.Match == RuleMatchAny {
		return slices.ContainsFunc(r.conds, func(c condition) bool { return c.match(in) })
	}
	return len(r.conds) > 0 && !slices.ContainsFunc(r.conds, func(c condition) bool { return !c.match(in) })
}

// apply does the action a to in and reports whether in changed.
func (rs RuleSet) apply(in *OperationInput, a Action) bool {
	switch a.Type {
	case ActionSetCategory:
		id, err := strconv.ParseInt(a.Value, 10, 64)
		if err != nil {
			return false
		}
		cat, ok := rs.cats[id]
		if !ok || cat.Archived || !slices.Contains([]OpType{OpExpense, OpIncome}, in.Type) ||
			cat.Kind != (Operation{Type: in.Type}).Kind() || in.CategoryID == id {
			return false
		}
		in.CategoryID = id
	case ActionSetPayee:
		if in.Payee == a.Value {
			return false
		}
		in.Payee = a.Value
	case ActionSetNote:
		if in.Note == a.Value {
			return false
		}
		in.Note = a.Value
	// notes already ending or starting with the value are left alone, so running
	// rules again does not repeat it
	case ActionAppendNote:
		if strings.HasSuffix(in.Note, a.Value) {
			return false
		}
		in.Note = strings.TrimSpace(in.Note + " " + a.Value)
	case ActionPrependNote:
		if strings.HasPrefix(in.Note, a.Value) {
			return false
		}
		in.Note = strings.TrimSpace(a.Value + " " + in.Note)
	case ActionAddTag:
		// a tag over the limit would fail the whole operation, so it is not added
		if len(in.Tags) >= _maxTags ||
			slices.ContainsFunc(in.Tags, func(t string) bool { return strings.EqualFold(t, a.Value) }) {
			return false
		}
		in.Tags = append(in.Tags, a.Value)
	default:
		return false
	}
	return true
}

// FillEmpty merges ruled, the result of rules run on user, into what the user entered
// for a new operation: the payee, note and category the user gave win, rules fill only
// the empty ones. Tags added by rules are kept.
func FillEmpty(user, ruled OperationInput) OperationInput {
	out := ruled
	if user.Payee != "" {
		out.Payee = user.Payee
	}
	if user.Note != "" {
		out.Note = user.Note
	}
	if user.CategoryID != 0 {
		out.CategoryID = user.CategoryID
	}
	return out
}

// LearnCategory learns from the user setting the category of changed, the way Actual
// Budget does. Among the 5 latest expenses and income of history, the operations with
// the payee of changed newest first, a category used at least 3 times becomes the
// category of the payee, if changed is one of them. It returns the rules to store:
// the default rules "payee is X → set category" of that kind, pointed at the category,
// or a new learned rule with ID 0 when there are none. It returns false when there is
// nothing to change, also when a "payee one of" rule already sets the category. cats
// are the user's categories by id.
func LearnCategory(history []Operation, changed Operation, rules []Rule, cats map[int64]Category) ([]Rule, bool) {
	catID, ok := payeeCategory(history, changed)
	cat, found := cats[catID]
	if !ok || !found {
		return nil, false
	}

	value := strconv.FormatInt(catID, 10)
	payee := foldText(changed.Payee)
	var simple []Rule
	coveredByOneOf := false
	for _, r := range rules {
		if !setsPayeeCategory(r, cat.Kind, cats) {
			continue
		}
		c := r.Conditions[0]
		folded := make([]string, len(c.Values))
		for i, v := range c.Values {
			folded[i] = foldText(v)
		}
		switch {
		case r.Stage == RuleStageDefault && c.Op == CondIs && slices.Equal(folded, []string{payee}):
			simple = append(simple, r)
		case c.Op == CondOneOf && slices.Contains(folded, payee) && r.Actions[0].Value == value:
			coveredByOneOf = true
		}
	}
	if len(simple) == 0 {
		if coveredByOneOf {
			return nil, false
		}
		return []Rule{{
			Stage:      RuleStageDefault,
			Match:      RuleMatchAll,
			Conditions: []Condition{{Field: RuleFieldPayee, Op: CondIs, Values: []string{changed.Payee}}},
			Actions:    []Action{{Type: ActionSetCategory, Value: value}},
			Origin:     RuleOriginLearned,
		}}, true
	}
	var out []Rule
	for _, r := range simple {
		if r.Actions[0].Value == value {
			continue
		}
		r.Actions = []Action{{Type: ActionSetCategory, Value: value}}
		out = append(out, r)
	}
	return out, len(out) > 0
}

// payeeCategory returns the category used at least 3 times among the 5 latest
// expenses and income of history, if changed is one of them.
func payeeCategory(history []Operation, changed Operation) (int64, bool) {
	const (
		window  = 5
		minUses = 3
	)
	if changed.Payee == "" || changed.CategoryID == 0 {
		return 0, false
	}
	var latest []Operation
	for _, op := range history {
		if (op.Type == OpExpense || op.Type == OpIncome) && !op.IsSplit() && op.CategoryID != 0 {
			latest = append(latest, op)
		}
		if len(latest) == window {
			break
		}
	}
	if !slices.ContainsFunc(latest, func(op Operation) bool { return op.ID == changed.ID }) {
		return 0, false
	}
	uses := make(map[int64]int, len(latest))
	for _, op := range latest {
		if uses[op.CategoryID]++; uses[op.CategoryID] >= minUses {
			return op.CategoryID, true
		}
	}
	return 0, false
}

// setsPayeeCategory reports whether r has one condition on the payee and one action
// setting a category of kind.
func setsPayeeCategory(r Rule, kind Kind, cats map[int64]Category) bool {
	if len(r.Conditions) != 1 || r.Conditions[0].Field != RuleFieldPayee ||
		len(r.Actions) != 1 || r.Actions[0].Type != ActionSetCategory {
		return false
	}
	id, err := strconv.ParseInt(r.Actions[0].Value, 10, 64)
	return err == nil && cats[id].Kind == kind
}
