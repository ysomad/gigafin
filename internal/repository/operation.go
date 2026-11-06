package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/timex"
)

// Operations stores operations.
type Operations struct {
	q *sqlc.Queries
}

// Create stores a validated operation linked to an import row when ref is set and
// returns it as stored.
func (r *Operations) Create(
	ctx context.Context, userID int64, op domain.Operation, ref domain.ImportRef, now time.Time,
) (domain.Operation, error) {
	amount, err := op.Amount.ToMinor(op.Currency)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("operation amount: %w", err)
	}
	row, err := r.q.CreateOperation(ctx, sqlc.CreateOperationParams{
		UserID:      userID,
		Type:        string(op.Type),
		AccountID:   op.AccountID,
		ToAccountID: sql.NullInt64{Int64: op.ToAccountID, Valid: op.ToAccountID != 0},
		CategoryID:  sql.NullInt64{Int64: op.CategoryID, Valid: op.CategoryID != 0},
		OriginID:    sql.NullInt64{Int64: op.OriginID, Valid: op.OriginID != 0},
		Amount:      amount,
		Date:        timex.FormatDate(op.Date),
		Time:        sql.NullString{String: op.Time, Valid: op.Time != ""},
		Payee:       sql.NullString{String: op.Payee, Valid: op.Payee != ""},
		SourcePayee: sql.NullString{String: op.SourcePayee, Valid: op.SourcePayee != ""},
		Note:        sql.NullString{String: op.Note, Valid: op.Note != ""},
		MCC:         sql.NullString{String: op.MCC, Valid: op.MCC != ""},
		ImportID:    sql.NullInt64{Int64: ref.ImportID, Valid: ref.ImportID != 0},
		ImportHash:  sql.NullString{String: ref.Hash, Valid: ref.Hash != ""},
		CreatedAt:   now.Unix(),
	})
	k, ok := constraint(err)
	if ok && k == errors.ErrExists {
		return domain.Operation{}, fmt.Errorf("operation already imported: %w", k)
	}
	if ok {
		return domain.Operation{}, fmt.Errorf("create operation: %w", k)
	}
	if err != nil {
		return domain.Operation{}, fmt.Errorf("create operation: %w", err)
	}
	if err := r.setTags(ctx, userID, row.ID, op.Tags, now); err != nil {
		return domain.Operation{}, err
	}
	created, err := operationFromRow(row, op.Currency)
	if err != nil {
		return domain.Operation{}, err
	}
	created.Tags = op.Tags
	return created, nil
}

// Update replaces the operation with id op.ID, keeping its MCC and source payee, and
// returns it as stored.
func (r *Operations) Update(
	ctx context.Context, userID int64, op domain.Operation, now time.Time,
) (domain.Operation, error) {
	amount, err := op.Amount.ToMinor(op.Currency)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("operation amount: %w", err)
	}
	row, err := r.q.UpdateOperation(ctx, sqlc.UpdateOperationParams{
		Type:        string(op.Type),
		AccountID:   op.AccountID,
		ToAccountID: sql.NullInt64{Int64: op.ToAccountID, Valid: op.ToAccountID != 0},
		CategoryID:  sql.NullInt64{Int64: op.CategoryID, Valid: op.CategoryID != 0},
		OriginID:    sql.NullInt64{Int64: op.OriginID, Valid: op.OriginID != 0},
		Amount:      amount,
		Date:        timex.FormatDate(op.Date),
		Time:        sql.NullString{String: op.Time, Valid: op.Time != ""},
		Payee:       sql.NullString{String: op.Payee, Valid: op.Payee != ""},
		Note:        sql.NullString{String: op.Note, Valid: op.Note != ""},
		UpdatedAt:   sql.NullInt64{Int64: now.Unix(), Valid: true},
		ID:          op.ID,
		UserID:      userID,
	})
	if k, ok := constraint(err); ok {
		return domain.Operation{}, fmt.Errorf("update operation: %w", k)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, fmt.Errorf("update operation: %w", errors.ErrNotFound)
	}
	if err != nil {
		return domain.Operation{}, fmt.Errorf("update operation: %w", err)
	}
	if err := r.setTags(ctx, userID, op.ID, op.Tags, now); err != nil {
		return domain.Operation{}, err
	}
	updated, err := operationFromRow(row, op.Currency)
	if err != nil {
		return domain.Operation{}, err
	}
	updated.Tags = op.Tags
	return updated, nil
}

// setTags replaces the tags of the operation id, creating new ones and deleting the
// ones no operation has any more.
func (r *Operations) setTags(ctx context.Context, userID, id int64, tags []string, now time.Time) error {
	if err := r.q.DeleteOperationTags(ctx, id); err != nil {
		return fmt.Errorf("delete operation tags: %w", err)
	}
	for _, name := range tags {
		tagID, err := r.q.UpsertTag(ctx, sqlc.UpsertTagParams{UserID: userID, Name: name, CreatedAt: now.Unix()})
		if err != nil {
			return fmt.Errorf("upsert tag: %w", err)
		}
		if err := r.q.CreateOperationTag(ctx, sqlc.CreateOperationTagParams{OperationID: id, TagID: tagID}); err != nil {
			return fmt.Errorf("create operation tag: %w", err)
		}
	}
	if err := r.q.DeleteUnusedTags(ctx, userID); err != nil {
		return fmt.Errorf("delete unused tags: %w", err)
	}
	return nil
}

// loadTags fills the tags of ops.
func (r *Operations) loadTags(ctx context.Context, ops []domain.Operation) error {
	if len(ops) == 0 {
		return nil
	}
	ids := make([]int64, len(ops))
	for i, op := range ops {
		ids[i] = op.ID
	}
	rows, err := r.q.ListOperationTags(ctx, ids)
	if err != nil {
		return fmt.Errorf("list operation tags: %w", err)
	}
	byOp := make(map[int64][]string, len(ops))
	for _, row := range rows {
		byOp[row.OperationID] = append(byOp[row.OperationID], row.Name)
	}
	for i := range ops {
		ops[i].Tags = byOp[ops[i].ID]
	}
	return nil
}

// Tags returns the names of the user's tags, sorted.
func (r *Operations) Tags(ctx context.Context, userID int64) ([]string, error) {
	tags, err := r.q.ListTags(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

// DateRange returns the dates of the user's first and last operations, zero when there
// are none.
func (r *Operations) DateRange(ctx context.Context, userID int64) (first, last time.Time, err error) {
	row, err := r.q.OperationDateRange(ctx, userID)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("operation date range: %w", err)
	}
	if row.FirstDate == "" {
		return time.Time{}, time.Time{}, nil
	}
	if first, err = timex.ParseDate(row.FirstDate); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("first date %q: %w", row.FirstDate, err)
	}
	if last, err = timex.ParseDate(row.LastDate); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("last date %q: %w", row.LastDate, err)
	}
	return first, last, nil
}

// Get returns one operation.
func (r *Operations) Get(ctx context.Context, userID, id int64) (domain.Operation, error) {
	row, err := r.q.GetOperation(ctx, sqlc.GetOperationParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Operation{}, notFound(err, "operation")
	}
	op, err := operationFromRow(row.Operation, row.Currency)
	if err != nil {
		return domain.Operation{}, err
	}
	ops := []domain.Operation{op}
	if err := r.loadTags(ctx, ops); err != nil {
		return domain.Operation{}, err
	}
	return ops[0], nil
}

// ByPayee returns the operations with exactly payee dated between from and to, both
// included, newest first, without tags.
func (r *Operations) ByPayee(
	ctx context.Context, userID int64, payee string, from, to time.Time,
) ([]domain.Operation, error) {
	rows, err := r.q.ListOperationsByPayee(ctx, sqlc.ListOperationsByPayeeParams{
		UserID:   userID,
		Payee:    sql.NullString{String: payee, Valid: true},
		DateFrom: timex.FormatDate(from),
		DateTo:   timex.FormatDate(to),
	})
	if err != nil {
		return nil, fmt.Errorf("list operations by payee: %w", err)
	}
	ops := make([]domain.Operation, len(rows))
	for i, row := range rows {
		if ops[i], err = operationFromRow(row.Operation, row.Currency); err != nil {
			return nil, err
		}
	}
	return ops, nil
}

// List returns up to limit operations matching f, newest first, starting past the
// cursor unless it is zero.
func (r *Operations) List(
	ctx context.Context, userID int64, f domain.Filter, cursor pagination.Cursor, limit int,
) ([]domain.Operation, error) {
	fp, err := filterParams(userID, f)
	if err != nil {
		return nil, err
	}
	params := sqlc.ListOperationsParams{
		UserID:     fp.UserID,
		AccountID:  fp.AccountID,
		CategoryID: fp.CategoryID,
		Type:       fp.Type,
		DateFrom:   fp.DateFrom,
		DateTo:     fp.DateTo,
		Query:      fp.Query,
		Tags:       fp.Tags,
		AmountMin:  fp.AmountMin,
		AmountMax:  fp.AmountMax,
		Lim:        int64(limit),
	}
	if !cursor.IsZero() {
		params.BeforeDate = sql.NullString{String: timex.FormatDate(cursor.Date), Valid: true}
		params.BeforeID = sql.NullInt64{Int64: cursor.ID, Valid: true}
	}
	rows, err := r.q.ListOperations(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}
	ops := make([]domain.Operation, len(rows))
	for i, row := range rows {
		op, err := operationFromRow(row.Operation, row.Currency)
		if err != nil {
			return nil, err
		}
		ops[i] = op
	}
	if err := r.loadTags(ctx, ops); err != nil {
		return nil, err
	}
	return ops, nil
}

// Sum totals operations matching f per type and currency.
func (r *Operations) Sum(ctx context.Context, userID int64, f domain.Filter) ([]domain.TypeSum, error) {
	params, err := filterParams(userID, f)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.SumFilteredOperations(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("sum filtered operations: %w", err)
	}
	out := make([]domain.TypeSum, len(rows))
	for i, s := range rows {
		out[i] = domain.TypeSum{
			Type: domain.OpType(s.Type), Currency: s.Currency, Total: money.FromMinor(s.Total, s.Currency),
		}
	}
	return out, nil
}

// filterParams converts f to the conditions shared by ListOperations and
// SumFilteredOperations.
func filterParams(userID int64, f domain.Filter) (sqlc.SumFilteredOperationsParams, error) {
	// every word of the query must match the start of a word of payee or note; words
	// are quoted so FTS5 syntax in them is taken literally, and ё is е as in the index
	words := strings.Fields(strings.NewReplacer("ё", "е", "Ё", "Е").Replace(f.Query))
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"*`
	}
	query := strings.Join(words, " ")
	// amounts are minor units of each account currency, so a bound becomes minor units
	// of every currency, as a JSON object by code; min rounds up and max down
	var bounds [2]sql.NullString
	for i, b := range []money.NullAmount{f.AmountMin, f.AmountMax} {
		if !b.Valid {
			continue
		}
		minor := make(map[string]int64)
		for _, cur := range money.Currencies() {
			if i == 0 {
				minor[cur] = b.Amount.CeilMinor(cur)
			} else {
				minor[cur] = b.Amount.FloorMinor(cur)
			}
		}
		js, err := json.Marshal(minor)
		if err != nil {
			return sqlc.SumFilteredOperationsParams{}, fmt.Errorf("amount bound: %w", err)
		}
		bounds[i] = sql.NullString{String: string(js), Valid: true}
	}
	return sqlc.SumFilteredOperationsParams{
		UserID:     userID,
		AccountID:  sql.NullInt64{Int64: f.AccountID, Valid: f.AccountID != 0},
		CategoryID: sql.NullInt64{Int64: f.CategoryID, Valid: f.CategoryID != 0},
		Type:       sql.NullString{String: string(f.Type), Valid: f.Type != ""},
		DateFrom:   sql.NullString{String: timex.FormatDate(f.DateFrom), Valid: !f.DateFrom.IsZero()},
		DateTo:     sql.NullString{String: timex.FormatDate(f.DateTo), Valid: !f.DateTo.IsZero()},
		Query:      sql.NullString{String: query, Valid: query != ""},
		Tags:       sql.NullString{String: "," + strings.Join(f.Tags, ",") + ",", Valid: len(f.Tags) > 0},
		AmountMin:  bounds[0],
		AmountMax:  bounds[1],
	}, nil
}

// Delete deletes an operation.
func (r *Operations) Delete(ctx context.Context, userID, id int64) error {
	n, err := r.q.DeleteOperation(ctx, sqlc.DeleteOperationParams{ID: id, UserID: userID})
	if err := affected(n, err, "delete operation"); err != nil {
		return err
	}
	if err := r.q.DeleteUnusedTags(ctx, userID); err != nil {
		return fmt.Errorf("delete unused tags: %w", err)
	}
	return nil
}

// DeleteImport deletes the operations an import created and returns how many.
func (r *Operations) DeleteImport(ctx context.Context, userID, importID int64) (int64, error) {
	n, err := r.q.DeleteImportOperations(ctx, sqlc.DeleteImportOperationsParams{
		ImportID: sql.NullInt64{Int64: importID, Valid: importID != 0}, UserID: userID,
	})
	if err != nil {
		return 0, fmt.Errorf("delete import operations: %w", err)
	}
	if err := r.q.DeleteUnusedTags(ctx, userID); err != nil {
		return 0, fmt.Errorf("delete unused tags: %w", err)
	}
	return n, nil
}

// DeleteAll deletes every operation and tag of the user.
func (r *Operations) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserOperations(ctx, userID); err != nil {
		return fmt.Errorf("delete operations: %w", err)
	}
	if err := r.q.DeleteUserTags(ctx, userID); err != nil {
		return fmt.Errorf("delete tags: %w", err)
	}
	return nil
}

// SumByCategory totals categorized operations per category and currency between the dates,
// inclusive.
func (r *Operations) SumByCategory(
	ctx context.Context, userID int64, from, to time.Time,
) ([]domain.CategorySum, error) {
	rows, err := r.q.SumOperationsByCategory(ctx, sqlc.SumOperationsByCategoryParams{
		UserID: userID, DateFrom: timex.FormatDate(from), DateTo: timex.FormatDate(to),
	})
	if err != nil {
		return nil, fmt.Errorf("sum operations by category: %w", err)
	}
	out := make([]domain.CategorySum, len(rows))
	for i, s := range rows {
		out[i] = domain.CategorySum{
			CategoryID: s.CategoryID, Currency: s.Currency, Total: money.FromMinor(s.Total, s.Currency),
		}
	}
	return out, nil
}

// SumByMonth totals operations per month, type and currency between the dates,
// inclusive.
func (r *Operations) SumByMonth(
	ctx context.Context, userID int64, from, to time.Time,
) ([]domain.MonthSum, error) {
	rows, err := r.q.SumOperationsByMonth(ctx, sqlc.SumOperationsByMonthParams{
		UserID: userID, DateFrom: timex.FormatDate(from), DateTo: timex.FormatDate(to),
	})
	if err != nil {
		return nil, fmt.Errorf("sum operations by month: %w", err)
	}
	out := make([]domain.MonthSum, len(rows))
	for i, s := range rows {
		month, err := timex.ParseMonth(s.Month)
		if err != nil {
			return nil, fmt.Errorf("month %q: %w", s.Month, err)
		}
		out[i] = domain.MonthSum{
			Month: month, Type: domain.OpType(s.Type), Currency: s.Currency,
			Total: money.FromMinor(s.Total, s.Currency),
		}
	}
	return out, nil
}

// SumByCategoryMonth totals categorized operations per category, month and currency
// between the dates, inclusive.
func (r *Operations) SumByCategoryMonth(
	ctx context.Context, userID int64, from, to time.Time,
) ([]domain.CategoryMonthSum, error) {
	rows, err := r.q.SumOperationsByCategoryMonth(ctx, sqlc.SumOperationsByCategoryMonthParams{
		UserID: userID, DateFrom: timex.FormatDate(from), DateTo: timex.FormatDate(to),
	})
	if err != nil {
		return nil, fmt.Errorf("sum operations by category and month: %w", err)
	}
	out := make([]domain.CategoryMonthSum, len(rows))
	for i, s := range rows {
		month, err := timex.ParseMonth(s.Month)
		if err != nil {
			return nil, fmt.Errorf("month %q: %w", s.Month, err)
		}
		out[i] = domain.CategoryMonthSum{
			CategoryID: s.CategoryID, Month: month, Currency: s.Currency,
			Total: money.FromMinor(s.Total, s.Currency),
		}
	}
	return out, nil
}

// SumByPayee totals operations with a payee per payee, type and currency between the
// dates, inclusive.
func (r *Operations) SumByPayee(
	ctx context.Context, userID int64, from, to time.Time,
) ([]domain.PayeeSum, error) {
	rows, err := r.q.SumOperationsByPayee(ctx, sqlc.SumOperationsByPayeeParams{
		UserID: userID, DateFrom: timex.FormatDate(from), DateTo: timex.FormatDate(to),
	})
	if err != nil {
		return nil, fmt.Errorf("sum operations by payee: %w", err)
	}
	out := make([]domain.PayeeSum, len(rows))
	for i, s := range rows {
		out[i] = domain.PayeeSum{
			Payee: s.Payee, Type: domain.OpType(s.Type), Currency: s.Currency,
			Total: money.FromMinor(s.Total, s.Currency), Count: s.Ops,
		}
	}
	return out, nil
}

// BalanceChanges returns how much the balance of each account changed per month up to
// the end of date to, oldest month first.
func (r *Operations) BalanceChanges(ctx context.Context, userID int64, to time.Time) ([]domain.BalanceChange, error) {
	rows, err := r.q.SumBalanceChangesByMonth(ctx, sqlc.SumBalanceChangesByMonthParams{
		UserID: userID, DateTo: timex.FormatDate(to),
	})
	if err != nil {
		return nil, fmt.Errorf("sum balance changes by month: %w", err)
	}
	out := make([]domain.BalanceChange, len(rows))
	for i, s := range rows {
		month, err := timex.ParseMonth(s.Month)
		if err != nil {
			return nil, fmt.Errorf("month %q: %w", s.Month, err)
		}
		out[i] = domain.BalanceChange{
			Month: month, AccountID: s.AccountID, Currency: s.Currency,
			Total: money.FromMinor(s.Total, s.Currency),
		}
	}
	return out, nil
}

func operationFromRow(row sqlc.Operation, currency string) (domain.Operation, error) {
	date, err := timex.ParseDate(row.Date)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("operation %d date %q: %w", row.ID, row.Date, err)
	}
	op := domain.Operation{
		ID:          row.ID,
		Type:        domain.OpType(row.Type),
		AccountID:   row.AccountID,
		ToAccountID: row.ToAccountID.Int64,
		CategoryID:  row.CategoryID.Int64,
		OriginID:    row.OriginID.Int64,
		Currency:    currency,
		Amount:      money.FromMinor(row.Amount, currency),
		Date:        date,
		Time:        row.Time.String,
		Payee:       row.Payee.String,
		SourcePayee: row.SourcePayee.String,
		Note:        row.Note.String,
		MCC:         row.MCC.String,
		ImportID:    row.ImportID.Int64,
		CreatedAt:   time.Unix(row.CreatedAt, 0),
	}
	if row.UpdatedAt.Valid {
		op.UpdatedAt = time.Unix(row.UpdatedAt.Int64, 0)
	}
	return op, nil
}
