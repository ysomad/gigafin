package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
)

// Imports stores imports, their staged rows and bank-to-own mappings.
type Imports struct {
	q *sqlc.Queries
}

// Create stores a new staged import.
func (r *Imports) Create(
	ctx context.Context, userID int64, src domain.ImportSource, filename string, now time.Time,
) (domain.Import, error) {
	im, err := r.q.CreateImport(ctx, sqlc.CreateImportParams{
		UserID: userID, Source: string(src), Filename: filename, Status: string(domain.ImportStaged),
		CreatedAt: now.Unix(),
	})
	if err != nil {
		return domain.Import{}, fmt.Errorf("create import: %w", err)
	}
	return importFromRow(im), nil
}

// Get returns one import.
func (r *Imports) Get(ctx context.Context, userID, id int64) (domain.Import, error) {
	im, err := r.q.GetImport(ctx, sqlc.GetImportParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Import{}, notFound(err, "import")
	}
	return importFromRow(im), nil
}

// List returns recent imports, newest first.
func (r *Imports) List(ctx context.Context, userID int64) ([]domain.Import, error) {
	const limit = 50
	rows, err := r.q.ListImports(ctx, sqlc.ListImportsParams{UserID: userID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list imports: %w", err)
	}
	out := make([]domain.Import, len(rows))
	for i, im := range rows {
		out[i] = importFromRow(im)
	}
	return out, nil
}

// Update stores the status and row counts of im.
func (r *Imports) Update(ctx context.Context, userID int64, im domain.Import) error {
	n, err := r.q.UpdateImport(ctx, sqlc.UpdateImportParams{
		Status:    string(im.Status),
		RowsTotal: im.RowsTotal,
		RowsNew:   im.RowsNew,
		RowsDup:   im.RowsDup,
		RowsSkip:  im.RowsSkip,
		ID:        im.ID,
		UserID:    userID,
	})
	return affected(n, err, "update import")
}

// Delete deletes an import with its rows.
func (r *Imports) Delete(ctx context.Context, userID, id int64) error {
	n, err := r.q.DeleteImport(ctx, sqlc.DeleteImportParams{ID: id, UserID: userID})
	return affected(n, err, "delete import")
}

// CreateRow stages a row of an import and returns its id.
func (r *Imports) CreateRow(ctx context.Context, importID int64, row domain.ImportRow) (int64, error) {
	payload, err := json.Marshal(row.Row)
	if err != nil {
		return 0, fmt.Errorf("marshal row: %w", err)
	}
	id, err := r.q.CreateImportRow(ctx, sqlc.CreateImportRowParams{
		ImportID:   importID,
		Line:       int64(row.Row.Line),
		Payload:    string(payload),
		ImportHash: row.Hash,
		Status:     string(row.Status),
	})
	if err != nil {
		return 0, fmt.Errorf("create row: %w", err)
	}
	return id, nil
}

// ListRows returns the rows of an import in file order.
func (r *Imports) ListRows(ctx context.Context, importID int64) ([]domain.ImportRow, error) {
	rows, err := r.q.ListImportRows(ctx, importID)
	if err != nil {
		return nil, fmt.Errorf("list rows: %w", err)
	}
	out := make([]domain.ImportRow, len(rows))
	for i, row := range rows {
		var rec domain.Record
		if err := json.Unmarshal([]byte(row.Payload), &rec); err != nil {
			return nil, fmt.Errorf("unmarshal row %d: %w", row.ID, err)
		}
		out[i] = domain.ImportRow{
			ID:          row.ID,
			Hash:        row.ImportHash,
			Row:         rec,
			Status:      domain.ImportRowStatus(row.Status),
			PairID:      row.PairRowID.Int64,
			OperationID: row.OperationID.Int64,
		}
	}
	return out, nil
}

// SetPair links row id to pairID, or unlinks it when pairID is 0.
func (r *Imports) SetPair(ctx context.Context, id, pairID int64) error {
	if err := r.q.SetImportRowPair(ctx, sqlc.SetImportRowPairParams{
		PairRowID: sql.NullInt64{Int64: pairID, Valid: pairID != 0}, ID: id,
	}); err != nil {
		return fmt.Errorf("pair rows: %w", err)
	}
	return nil
}

// SetOperation records the operation a row created.
func (r *Imports) SetOperation(ctx context.Context, id, opID int64) error {
	err := r.q.SetImportRowOperation(ctx, sqlc.SetImportRowOperationParams{
		OperationID: sql.NullInt64{Int64: opID, Valid: true}, ID: id,
	})
	if err != nil {
		return fmt.Errorf("link row: %w", err)
	}
	return nil
}

// ImportedHashes returns which of hashes belong to new rows of committed imports. A
// row stays imported after its operation is deleted, so it does not come back.
func (r *Imports) ImportedHashes(ctx context.Context, userID int64, hashes []string) (map[string]bool, error) {
	// keeps IN lists below SQLite's variable limit
	const chunkSize = 500
	out := make(map[string]bool, len(hashes))
	for chunk := range slices.Chunk(hashes, chunkSize) {
		found, err := r.q.ListImportedHashes(ctx, sqlc.ListImportedHashesParams{
			UserID:       userID,
			ImportStatus: string(domain.ImportCommitted),
			RowStatus:    string(domain.ImportRowNew),
			Hashes:       chunk,
		})
		if err != nil {
			return nil, fmt.Errorf("list imported hashes: %w", err)
		}
		for _, h := range found {
			out[h] = true
		}
	}
	return out, nil
}

// Mapping returns the saved bank-to-own mappings of src. Mapped accounts have no
// balance.
func (r *Imports) Mapping(ctx context.Context, userID int64, src domain.ImportSource) (domain.ImportMapping, error) {
	accs, err := r.q.ListAccountMap(ctx, sqlc.ListAccountMapParams{UserID: userID, Source: string(src)})
	if err != nil {
		return domain.ImportMapping{}, fmt.Errorf("list account map: %w", err)
	}
	cats, err := r.q.ListCategoryMap(ctx, sqlc.ListCategoryMapParams{UserID: userID, Source: string(src)})
	if err != nil {
		return domain.ImportMapping{}, fmt.Errorf("list category map: %w", err)
	}
	m := domain.ImportMapping{
		Accounts:   make(map[string]domain.Account, len(accs)),
		Categories: make(map[string]domain.Category, len(cats)),
	}
	for _, am := range accs {
		key := domain.AccountKey(am.SourceName, am.MapCurrency)
		m.Accounts[key] = accountFromRow(am.Account, 0 /* balance */)
	}
	for _, cm := range cats {
		m.Categories[domain.CategoryKey(domain.Kind(cm.MapKind), cm.SourceName)] = categoryFromRow(cm.Category)
	}
	return m, nil
}

// MapAccount maps a bank account name in a currency to an own account.
func (r *Imports) MapAccount(
	ctx context.Context,
	userID int64,
	src domain.ImportSource,
	name, currency string,
	accountID int64,
) error {
	err := r.q.UpsertAccountMap(ctx, sqlc.UpsertAccountMapParams{
		UserID:     userID,
		Source:     string(src),
		SourceName: name,
		Currency:   currency,
		AccountID:  accountID,
	})
	if k, ok := constraint(err); ok {
		return fmt.Errorf("save account map: %w", k)
	}
	if err != nil {
		return fmt.Errorf("save account map: %w", err)
	}
	return nil
}

// MapCategory maps a bank category of a kind to an own category.
func (r *Imports) MapCategory(
	ctx context.Context, userID int64, src domain.ImportSource, name string, kind domain.Kind, categoryID int64,
) error {
	err := r.q.UpsertCategoryMap(ctx, sqlc.UpsertCategoryMapParams{
		UserID: userID, Source: string(src), SourceName: name, Kind: string(kind), CategoryID: categoryID,
	})
	if k, ok := constraint(err); ok {
		return fmt.Errorf("save category map: %w", k)
	}
	if err != nil {
		return fmt.Errorf("save category map: %w", err)
	}
	return nil
}

// DeleteAll deletes every import and mapping of the user. Operations must be deleted
// first.
func (r *Imports) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserImports(ctx, userID); err != nil {
		return fmt.Errorf("delete imports: %w", err)
	}
	if err := r.q.DeleteUserAccountMaps(ctx, userID); err != nil {
		return fmt.Errorf("delete account mappings: %w", err)
	}
	if err := r.q.DeleteUserCategoryMaps(ctx, userID); err != nil {
		return fmt.Errorf("delete category mappings: %w", err)
	}
	return nil
}

func importFromRow(im sqlc.Import) domain.Import {
	return domain.Import{
		ID:        im.ID,
		Source:    domain.ImportSource(im.Source),
		Filename:  im.Filename,
		Status:    domain.ImportStatus(im.Status),
		RowsTotal: im.RowsTotal,
		RowsNew:   im.RowsNew,
		RowsDup:   im.RowsDup,
		RowsSkip:  im.RowsSkip,
		CreatedAt: time.Unix(im.CreatedAt, 0),
	}
}
