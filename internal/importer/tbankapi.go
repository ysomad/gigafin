package importer

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/tbank"
)

// FromTBank converts operations of the T-Bank API into records equal to the rows of a
// CSV export of the same operations, so both dedupe against each other. Times are
// returned in loc. Operations of the accounts linked, by id, are imported; of the
// accounts mapped, not linked but mapped to own accounts, only those that are the other
// side of a transfer with a linked account, so the two pair into a transfer. The rest
// are skipped, as are failed operations, zero amounts and holds not yet debited, which
// may still change.
//
//nolint:cyclop // one switch of skip reasons and one pairing loop
func FromTBank(
	accounts []tbank.Account, linked, mapped []string, ops []tbank.Operation, loc *time.Location,
) domain.ParseResult {
	byID := make(map[string]tbank.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	var (
		res domain.ParseResult
		// others are the operations of accounts mapped, kept if they pair
		others []domain.Record
	)
	for i, op := range ops {
		category := op.SpendingCategory.Name
		if category == "" {
			category = _tbankDefaultCategory
		}
		note := op.Message
		if note == "" {
			note = op.Payment.Comment
		}
		acc, ok := byID[op.Account]
		row := domain.Record{
			Line:     i + 1,
			Account:  acc.Name,
			Time:     op.Time().Truncate(time.Second).In(loc),
			Amount:   op.Amount(),
			Currency: strings.ToUpper(acc.Currency.Name),
			Category: category,
			Payee:    strings.TrimSpace(op.Description),
			Note:     strings.TrimSpace(note),
		}
		if domain.ValidMCC(op.MCC) {
			row.MCC = op.MCC
		}
		switch {
		case !ok:
			row.SkipReason = "account not tracked"
		case op.Status != tbank.OpStatusOK:
			row.SkipReason = "status " + strconv.Quote(op.Status)
		case row.Amount.IsZero():
			row.SkipReason = "zero amount"
		case op.Pending():
			row.SkipReason = "not debited yet"
		case !slices.Contains(linked, op.Account) && slices.Contains(mapped, op.Account):
			others = append(others, row)
			continue
		case !slices.Contains(linked, op.Account):
			row.SkipReason = "account not linked"
		}
		if row.SkipReason != "" {
			res.Skipped = append(res.Skipped, row)
			continue
		}
		res.Rows = append(res.Rows, row)
	}

	// each operation of a linked account is the side of one transfer at most
	n := len(res.Rows)
	used := make([]bool, n)
	for _, o := range others {
		match := -1
		for i, r := range res.Rows[:n] {
			if !used[i] && r.Currency == o.Currency && r.Amount.Equal(o.Amount.Neg()) &&
				r.Time.Sub(o.Time).Abs() <= domain.TransferWindow {
				match = i
				break
			}
		}
		if match < 0 {
			o.SkipReason = "account not linked"
			res.Skipped = append(res.Skipped, o)
			continue
		}
		used[match] = true
		res.Rows = append(res.Rows, o)
	}
	return res
}
