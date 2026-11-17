# Rules: review findings and plan for change history with undo

## 1. How rules run today

| Trigger | Entry point | What runs | Overrides user data? | Recorded? |
|---|---|---|---|---|
| New operation (UI form, API, MCP) | `Operations.Create` → `Fill` → `Rules.Categorize` → `domain.FillEmpty` | full rule set; payee/note/category kept when user gave them, rule tags always added | no (fills empty only) | no |
| Live form fill | `ui/operations.fillOperation` → `Operations.Fill` | same as above, result put back in the form | no | no |
| File import commit | `Imports.commit` → `Imports.categorize` | full rule set on every non-split row | **yes**, beats the mapped category | no |
| Bank sync | `Banks.run` → `Imports.commit(uncategorized)` | same as import, rows start in "Uncategorized" | yes (intended) | no |
| "Run rules" on a filtered list | `Operations.Categorize` (UI + API) | full rule set over all matching ops | **yes**, overwrites manual edits | no |
| "Apply" one rule | `Rules.Apply` (UI + API) | that one rule alone, no stages/other rules | **yes**, over the whole history | no |
| Learning | `learnCategory` from `Operations.Create`/`Update` | creates or re-points "payee is X → category" rules | rewrites **user** rules too | no |

Nothing records which rule changed what, so a bad rule run over the full history
cannot be seen or reverted.

## 2. Review findings

### 2.1 Triggering / behaviour

1. **Learning feeds on its own output.** `fillOperation` puts the rule-chosen category
   into the form; on submit `in.CategoryID != 0`, so `Create` treats it as a manual pick
   and calls `learnCategory`. `payeeCategory` also counts history ops whose category came
   from rules, import mapping or bank "Uncategorized". Learning should count only
   categories the user picked (needs provenance, see §3) and `Create` should learn only
   when the submitted category differs from what `Fill` returned.
2. **Learning rewrites user rules.** `domain.LearnCategory` re-points every default
   "payee is X → set category" rule, including `RuleOriginUser` ones, and keeps
   `Origin` as is, so a hand-made rule silently changes. Re-point only
   `RuleOriginLearned` rules; when a user rule covers the payee, do nothing.
3. **Learning lookup is case-sensitive, rules are not.** `ListOperationsByPayee` uses
   `o.payee = ?`; rules compare `foldText`. "STARBUCKS" and "Starbucks" never reach 3
   uses together. Fold in SQL (`lower()` is ASCII-only in SQLite, so filter in Go over
   the date window) or compare folded payees in `payeeCategory`.
4. **Update with only a payee change triggers learning** of a category the user did not
   touch (`op.CategoryID == old.CategoryID && op.Payee == old.Payee` gate). Learn only
   when the category changed.
5. **"Run rules" / "Apply" overwrite manual edits with no way back.** Accepted
   behaviour in Actual Budget, but here there is no undo. Fixed by §3; optionally add
   a "skip operations edited by hand since last rule change" switch once provenance
   exists.
6. **`Rules.Apply` ignores stage and specificity.** It builds a one-rule set, so a
   more specific rule that would win in a full run is ignored. **Decision: keep "only
   this rule"**. Actual Budget ("Apply actions" in the rule editor), Firefly III
   ("apply rule to existing transactions"), Lunch Money and Monarch ("apply to existing
   transactions" when saving a rule) all apply just the one rule; a full run is a
   separate action everywhere (here: "Run rules" on the operations list). Pre-stage
   cleanup is already baked into stored payees, so stages matter little here. Fix only
   the UI: say "applies only this rule" next to the button, and make the preview count
   operations Apply would change (2.1.9), so preview and result agree.
7. **Lost updates.** `Rules.Apply` and `Operations.Categorize` read all operations from
   the read pool, compute, then `replaceOperations` writes the stale snapshot in a later
   transaction. A concurrent edit or a bank sync in between is overwritten. Re-read each
   op in the tx and skip it when `updated_at` changed (or recompute inside the tx; rules
   are fast — only a future LLM categorizer is slow).
8. **One bad operation aborts the whole run.** `replaceOperations` returns on the first
   `newOperation` failure ("operation N: ..."). Skip and count invalid ops instead,
   report `changed, skipped`.
9. **Preview vs Apply counts differ.** Preview counts matches (`RuleSet.Matches`); Apply
   counts changes (kind mismatch, archived category, already-set values are no-ops).
   Label the preview "matches", or count only ops `Apply` would change.
10. **`FillEmpty` mixes inputs.** Category may come from a default rule that matched the
    payee rewritten by a pre rule, while the user's own payee is kept. Low impact; note
    it in the doc comment or run rules on the user payee when one is given.
11. **Account conditions are not validated.** `NewRule` checks category ids against the
    user's categories but accepts any account id, so a deleted/foreign account makes a
    rule that silently never matches. Validate against the user's accounts like
    categories.
12. **Archived category targets are silent no-ops.** `checkAction` accepts an archived
    category; `RuleSet.apply` then skips it forever. Reject on save, and show an
    "inactive" badge in the list for existing ones.
13. **`append_note`/`prepend_note` idempotency check is too loose.** `HasSuffix(note,
    "bar")` treats "foobar" as already appended. Check `note == v || HasSuffix(note, " "+v)`.

### 2.2 Code logic / style

1. `Rules.Categorize` logs `WarnContext` for an invalid rule — CODE_STYLE allows WARN in
   services only for rejected API keys. Make `domain.NewRuleSet` skip invalid rules and
   return their ids (`(RuleSet, []int64 /* skipped */)`), log at DEBUG, surface them in
   the rules list as "invalid". This also removes the double compile (every rule is
   compiled twice today).
2. `Rules.Categorize` returns `ins, err` on error; return `nil, err`.
3. `Rules.Apply` does not skip invalid rules the way `Categorize` does → inconsistent;
   use the same path.
4. `RuleSet.Matches(id, in)` exists only for Preview and relies on a fresh rule having
   ID 0. Replace with compiling the single rule and matching it directly (e.g.
   `RuleSet.Match(in) []int64` returning matching rule ids — also useful for history).
5. `NewRuleSet` sort comparator calls `slices.Index(RuleStages(), …)` which allocates a
   slice per comparison; `compileCondition` calls `RuleFields()` twice; `exact` builds a
   `[]CondOp` per condition. Precompute stage index / score on `compiledRule`, use
   `CondOps(c.Field)`, use a `switch` for "exact".
6. `RuleSet.apply` uses `(Operation{Type: in.Type}).Kind()` and allocates
   `[]OpType{…}` per call; add `OpType.Kind()` or a direct switch.
7. **UI duplicates domain validation.** `parseRuleForm` re-implements value counts,
   money parse, regexp compile and length checks so it can key errors by row. Make
   `domain.NewRule` return a typed error carrying the row (`*RuleRowError{Kind: "cond"
   | "act", Index, Field, Err}`) and map it to `cond_value[i]`/`act_value[i]` in the
   handler; drop the duplicated checks.
8. `_ruleValueSep = "|"` means a one_of value can never contain `|` (payees do). Use one
   input per value or newline-separated textarea.
9. `Operations.Categorize` detects changes by comparing 4 fields; `Rules.Apply` uses
    the `changed` bool. After §3 both use the change list (`len(changes) > 0`).

## 3. Rule change history ("event sourcing") with undo

### 3.1 Scope

Full event sourcing (operations rebuilt from events) is not worth it for this app: the
`operations` table stays the source of truth. We add an **append-only journal of the
changes rules make**, grouped into runs, and implement undo as a **compensating run**
(new events, never deleting old ones). That gives: what changed, by which rule, when,
from which trigger, and safe undo with conflict detection.

Creates are recorded (trigger `create`) whatever the caller — UI, API or MCP — so
`Operations.Create` needs no caller flag. From the UI form the rule values are usually
already in the submitted input (`Fill` ran on the form), so `FillEmpty` reports no
changes and nothing is stored; API/MCP creates with empty fields get a run. Undo of a
created operation cannot clear a required category (`Before` is empty): that field is
reported as not revertible, payee/note/tags still revert.

History is kept forever, no pruning. An undo run cannot be undone.

### 3.2 Schema — `make migrate-create name=rule_history`

No defaults/checks, per project rules.

```sql
-- +goose Up
CREATE TABLE rule_runs (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trigger    TEXT    NOT NULL, -- import | bank_sync | run_rules | apply_rule | create | undo
    rule_id    INTEGER,          -- apply_rule only; no FK, the rule may be deleted
    import_id  INTEGER REFERENCES imports (id) ON DELETE SET NULL,
    undo_of    INTEGER REFERENCES rule_runs (id) ON DELETE SET NULL,
    undone_at  INTEGER,
    created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX rule_runs_user_idx ON rule_runs (user_id, id DESC);

CREATE TABLE rule_changes (
    id           INTEGER PRIMARY KEY,
    run_id       INTEGER NOT NULL REFERENCES rule_runs (id) ON DELETE CASCADE,
    operation_id INTEGER NOT NULL REFERENCES operations (id) ON DELETE CASCADE,
    rule_id      INTEGER NOT NULL, -- no FK, history outlives the rule
    action       TEXT    NOT NULL, -- domain.ActionType
    field        TEXT    NOT NULL, -- category | payee | note | tag
    before       TEXT,             -- category id / text / NULL; tag: NULL
    after        TEXT              -- category id / text; tag: tag name
) STRICT;
CREATE INDEX rule_changes_run_idx ON rule_changes (run_id, operation_id, id);
CREATE INDEX rule_changes_op_idx ON rule_changes (operation_id, id);

-- +goose Down
DROP TABLE rule_changes;
DROP TABLE rule_runs;
```

Change counts are queried (`COUNT(DISTINCT operation_id)`), not stored, so import
undo / operation delete (cascade) keep them right. Runs with no changes are not stored
(each bank sync would otherwise add an empty run).

`Data.DeleteAll`: add `r.RuleHistory.DeleteAll` before `r.Operations.DeleteAll`
(user cascade covers account deletion). Backup is the DB file, nothing to do.

### 3.3 Domain (`internal/domain/rule.go`, new `rule_history.go`)

```go
// RuleChange is one change a rule action made to an operation.
type RuleChange struct {
    RuleID int64
    Action ActionType
    Field  RuleChangeField // category, payee, note, tag
    Before string
    After  string
}

func (rs RuleSet) Apply(in OperationInput) (OperationInput, []RuleChange)
```

- `RuleSet.apply` returns `(RuleChange, bool)` per action; `Apply` collects them in
  order. Chained changes to one field (pre rule renames payee, post rule renames again)
  are all kept; undo uses the net change per field (first `Before`, last `After`).
- `FillEmpty(user, ruled OperationInput, changes []RuleChange) (OperationInput,
  []RuleChange)` drops changes for fields the user's value won.
- `type RuleTrigger string` with the trigger constants (start-at-one rule doesn't apply
  to string enums).
- `RuleRun{ID, Trigger, RuleID, ImportID, UndoOf, UndoneAt, CreatedAt, Ops int}`.
- `UndoChanges(current Operation, changes []RuleChange) (OperationInput, []RuleChange,
  bool)` — pure function, per operation:
  - net change per field; revert a field only if its current value equals the net
    `After` (else the user or a later run changed it → conflict, skip that field);
  - tag: remove it if still present;
  - returns the reverted input, the inverse changes (for the undo run), and whether
    anything was reverted.
  This is the bit to unit-test hardest (table tests: clean revert, conflicting field,
  chained payee rewrites, tag removed by user, category since deleted/archived).

### 3.4 Categorizer and service

- `Categorizer.Categorize` returns `[]domain.Categorized{In OperationInput; Changes
  []RuleChange}` so a future LLM categorizer reports changes the same way (`RuleID` 0,
  action as "llm" later).
- New repo `repository.RuleHistory` (+ `queries/rule_history.sql`): `CreateRun`,
  `AddChanges` (batch insert), `ListRuns(userID, cursor, limit)` with op counts,
  `GetRun`, `RunChanges(runID)`, `OperationChanges(userID, opID)`, `MarkUndone`,
  `DeleteAll`.
- Record in the **same transaction** as the operation writes:
  - `Imports.commit`: after `createOperation`, collect `(op.ID, changes)`; one run
    `import` or `bank_sync` (pass trigger from `Banks.run`), `import_id` set.
  - `replaceOperations` takes changes per op and a run header; used by
    `Operations.Categorize` (`run_rules`) and `Rules.Apply` (`apply_rule`, `rule_id`).
    Fold in fixes 2.1.7 (stale check) and 2.1.8 (skip invalid).
- `Rules.Undo(ctx, userID, runID) (reverted, conflicts int, err error)`:
  in one tx: load run (`ErrInvalid` when already undone or itself an `undo` run),
  load changes grouped by op, load current ops,
  `domain.UndoChanges`, `newOperation` + `Operations.Update`, write an `undo` run with
  the inverse changes, `MarkUndone`. Log INFO `rule run undone` with `user_id`,
  `count`. Undo of an `import`/`bank_sync` run reverts to the mapped/"Uncategorized"
  category — valid, since `Before` is always a real category there.
- `Rules.UndoOperation(ctx, userID, runID, opID)` — same, one operation (for the
  operation page).
- Learning (2.1.1): `payeeCategory` should ignore ops whose current category equals the
  latest rule change `After` for that op — query `OperationChanges` for the history ops
  (or one query by op ids).

### 3.5 UI (`internal/ui/rules`, Oat/native HTML only, no app.css/app.js)

- `GET /rules/history` — runs newest first, cursor-paginated: date, trigger label,
  rule link (apply_rule), import link, "N operations", "undone" badge, Undo button
  (`POST /rules/history/{id}/undo`, `hx-confirm`-style native `<form>` confirm the way
  other destructive buttons do it).
- `GET /rules/history/{id}` — operations of the run grouped by day (reuse
  `kit.GroupByDay`): per op, the field changes as "before → after" with category names
  and rule links; per-op Undo button.
- After undo redirect back with `?reverted=N&conflicts=M` and show a notice ("M
  operations were changed since and kept").
- Operation edit page (`internal/ui/operations`): "Changed by rules" `<details>` listing
  that op's changes with rule link and Undo.
- Rules list: link "History"; rule form: after Apply, link to the created run instead of
  just `?applied=N`.
- Strings to `web/locales/ru.json`.

### 3.6 API (optional, second step)

`GET /rule-runs`, `GET /rule-runs/{id}`, `POST /rule-runs/{id}/undo` in
`api/public.yaml`; `ApplyRule` and `Categorize` responses gain `run_id`. Then
`make generate`.

## 4. Order of work

1. Domain fixes that don't need history: 2.1.2, 2.1.3, 2.1.4, 2.1.11–13, 2.2.1–7.
   Tests in `domain/rule_test.go`, `service/rule_test.go`.
2. `RuleSet.Apply` returning changes; `Categorizer` signature; `FillEmpty` with changes.
3. Migration + sqlc queries + `repository.RuleHistory`; `make generate`.
4. Record runs in `Imports.commit`, `replaceOperations` (with 2.1.7/2.1.8),
   `Rules.Apply`, `Operations.Categorize`.
5. `domain.UndoChanges` + `Rules.Undo` / `UndoOperation` + service tests (temp SQLite).
6. Learning provenance (2.1.1).
7. UI pages + locale strings; then API.
8. `make generate test lint`.

## 5. Decisions

- API/MCP creates are recorded (all creates go through one path, see §3.1).
- No undo of an undo; undo runs show without a button.
- No pruning; history is kept forever.
- `Rules.Apply` keeps applying only its rule (2.1.6).
