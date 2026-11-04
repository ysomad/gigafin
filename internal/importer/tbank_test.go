package importer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
)

const _tbankHeader = `"Имя счёта";"Номер карты";"Дата операции";"Сумма операции";"Валюта операции";` +
	`"Сумма в валюте счёта";"Валюта счёта";"Статус";"Категория по-умолчанию";"Ваша категория";"MCC";` +
	`"Описание";"Сообщение";"Округление";"Сумма операции с округлением";"Бонусы (включая кэшбэк)";"Учёт в аналитике"`

func TestTBankParse(t *testing.T) {
	t.Parallel()

	csv := "\ufeff" + _tbankHeader + "\n" +
		`"Black";"*2581";"29.10.2026 18:48:38";"-1 040,50";"RUB";"-1 040,50";"RUB";"Ок";` +
		`"Супермаркеты";"";"5411";"Самокат";"";"0,00";"-1040,00";"10,00";"Да"` + "\n" +
		`"Black";"*2581";"29.10.2026 18:13:18";"-425,00";"RUB";"-425,00";"RUB";"FAILED";` +
		`"Такси";"";"4121";"Яндекс Такси";"";"0,00";"-425,00";"0,00";"Да"` + "\n" +
		"\n" +
		_tbankHeader + "\n" +
		`"Savings";"";"30.10.2026 09:00:00";"5000,00";"RUB";"5000,00";"RUB";"Ок";` +
		`"Пополнения";"Зарплата";"";"ООО Ромашка";"аванс";"";"";"";""` + "\n" +
		`"Savings";"";"30.10.2026 09:00:01";"0,00";"RUB";"0,00";"RUB";"Ок";"Другое";"";"";"Проверка";"";"";"";"";""` + "\n" +
		`"Savings";"";"bad";"oops";"RUB";"oops";"RUB";"FAILED";"Другое";"";"";"Сбой";"";"";"";"";""` + "\n"

	res, err := importer.Parse(domain.ImportSourceTBank, strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.Len(t, res.Skipped, 3, "failed status and zero amount")
	assert.Equal(t, 3, res.Skipped[0].Line)
	assert.Equal(t, `status "FAILED"`, res.Skipped[0].SkipReason)
	assert.Equal(t, "-425", res.Skipped[0].Amount.String())
	assert.Equal(t, "Яндекс Такси", res.Skipped[0].Payee)
	assert.Equal(t, "zero amount", res.Skipped[1].SkipReason)
	assert.Equal(t, "Проверка", res.Skipped[1].Payee)
	assert.Equal(t, `status "FAILED"`, res.Skipped[2].SkipReason, "malformed failed row is listed, not an error")
	assert.True(t, res.Skipped[2].Time.IsZero())
	assert.True(t, res.Skipped[2].Amount.IsZero())
	require.Len(t, res.Rows, 2)

	r := res.Rows[0]
	assert.Equal(t, 2, r.Line)
	assert.Equal(t, "Black", r.Account)
	assert.Equal(t, "-1040.5", r.Amount.String())
	assert.Equal(t, "RUB", r.Currency)
	assert.Equal(t, "Супермаркеты", r.Category)
	assert.Equal(t, "5411", r.MCC)
	assert.Equal(t, "Самокат", r.Payee)
	assert.Equal(t, time.Date(2026, 10, 29, 15, 48, 38, 0, time.UTC), r.Time, "Moscow is UTC+3, returned in loc")
	assert.Equal(t, "b10ca987538b087579b70e12a71e5a42", r.Hash(domain.ImportSourceTBank, 0),
		"hash must not change, or rows imported before are imported again")

	r = res.Rows[1]
	assert.Equal(t, "Зарплата", r.Category, "own category overrides the bank one")
	assert.Equal(t, "5000", r.Amount.String())
	assert.Equal(t, "аванс", r.Note)
}

func TestTBankParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		give   string
		want   string
		wantIs error
	}{
		{name: "empty", give: "", want: "empty file", wantIs: errors.ErrInvalid},
		{name: "no header", give: "a;b;c\n", want: "header", wantIs: errors.ErrInvalid},
		{
			name: "missing column", give: `"Имя счёта";"Статус"` + "\n",
			want: "missing column", wantIs: errors.ErrInvalid,
		},
		{name: "no operations", give: _tbankHeader + "\n", want: "no operations", wantIs: errors.ErrInvalid},
		{
			name: "bad amount",
			give: _tbankHeader + "\n" +
				`"Black";"";"29.10.2026 18:48:38";"x";"RUB";"x";"RUB";"Ок";"Такси";"";"";"Такси";"";"";"";"";""` + "\n",
			want:   "line 2",
			wantIs: errors.ErrInvalid,
		},
		{
			name: "bad time",
			give: _tbankHeader + "\n" +
				`"Black";"";"2026-10-29";"1";"RUB";"1";"RUB";"Ок";"Такси";"";"";"Такси";"";"";"";"";""` + "\n",
			want:   "DD.MM.YYYY",
			wantIs: errors.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := importer.Parse(domain.ImportSourceTBank, strings.NewReader(tt.give), time.UTC)
			require.ErrorContains(t, err, tt.want)
			require.ErrorIs(t, err, tt.wantIs)
		})
	}
}
