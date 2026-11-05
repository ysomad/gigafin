package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"filippo.io/age"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
)

// Data manages all finance data of a user at once.
type Data struct {
	store *repository.Store
	auth  *Auth
	log   *slog.Logger
}

// DeleteAll checks the user's current password and deletes every account, category,
// operation, plan, import and rule of the user in one transaction. The user, sessions
// and API keys are kept.
func (s *Data) DeleteAll(ctx context.Context, userID int64, pass string) error {
	if err := s.auth.CheckPassword(ctx, userID, pass); err != nil {
		return err
	}
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		// ordered so foreign keys hold: operations reference accounts and categories,
		// import mappings and plans reference them too
		steps := []func(context.Context, int64) error{
			r.Operations.DeleteAll,
			r.Imports.DeleteAll,
			r.Plans.DeleteAll,
			r.Rules.DeleteAll,
			r.Categories.DeleteAll,
			r.Accounts.DeleteAll,
		}
		for _, step := range steps {
			if err := step(ctx, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "all data deleted", slog.Int64("user_id", userID))
	return nil
}

// Export writes the user's operations matching the dates of f to w in the gigafin CSV
// format, oldest first.
func (s *Data) Export(ctx context.Context, userID int64, f domain.Filter, w io.Writer) error {
	const pageSize = 1000

	r := s.store.Read()

	accounts, err := r.Accounts.List(ctx, userID)
	if err != nil {
		return err
	}

	names := make(map[int64]string, len(accounts))
	for _, a := range accounts {
		names[a.ID] = a.Name
	}

	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return err
	}

	filter := domain.Filter{DateFrom: f.DateFrom, DateTo: f.DateTo}

	var (
		ops    []domain.Operation
		cursor pagination.Cursor
	)

	for {
		page, err := r.Operations.List(ctx, userID, filter, cursor, pageSize)
		if err != nil {
			return err
		}
		ops = append(ops, page...)
		if len(page) < pageSize {
			break
		}
		last := page[len(page)-1]
		cursor = pagination.Cursor{Date: last.Date, ID: last.ID}
	}

	slices.Reverse(ops)

	if err := importer.WriteGigaFin(w, ops, names, domain.CategoryNames(domain.NewTree(cats))); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "data exported", slog.Int64("user_id", userID), slog.Int("count", len(ops)))
	return nil
}

// Backup checks that u's role grants domain.PermBackup and the user's current password, then writes a copy
// of the whole database encrypted with age for passphrase into dir and returns it open
// for reading from the start; the caller closes it and removes dir. The unencrypted
// copy is removed on success; on failure it may be left in dir.
func (s *Data) Backup(ctx context.Context, u domain.User, pass, passphrase, dir string) (*os.File, error) {
	if !u.Can(domain.PermBackup) {
		return nil, domain.NewError(errors.ErrForbidden, "backup: forbidden")
	}

	if err := domain.CheckPasswordPolicy(passphrase); err != nil {
		return nil, fmt.Errorf("backup passphrase: %w", err)
	}

	if err := s.auth.CheckPassword(ctx, u.ID, pass); err != nil {
		return nil, err
	}

	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("age recipient: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open backup dir: %w", err)
	}
	defer root.Close()

	const plainName = "gigafin.db"
	if err := s.store.Backup(ctx, filepath.Join(dir, plainName)); err != nil {
		return nil, err
	}

	in, err := root.Open(plainName)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer in.Close()

	out, err := root.OpenFile(plainName+".age", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create encrypted backup: %w", err)
	}

	enc, err := age.Encrypt(out, recipient)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("encrypt backup: %w", err), out.Close())
	}

	if _, err := io.Copy(enc, in); err != nil {
		return nil, errors.Join(fmt.Errorf("encrypt backup: %w", err), out.Close())
	}

	// Close writes the last chunk, so its error means a truncated file
	if err := enc.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("encrypt backup: %w", err), out.Close())
	}

	// the plain copy holds every credential hash, so it goes as soon as it is encrypted
	if err := root.Remove(plainName); err != nil {
		return nil, errors.Join(fmt.Errorf("remove plain backup: %w", err), out.Close())
	}

	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Join(fmt.Errorf("rewind encrypted backup: %w", err), out.Close())
	}

	s.log.InfoContext(ctx, "backup created", slog.Int64("user_id", u.ID))
	return out, nil
}
