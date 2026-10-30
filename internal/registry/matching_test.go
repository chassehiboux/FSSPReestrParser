package registry

import (
	"context"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredKeysApplyOnlyToTheirFile(t *testing.T) {
	for _, known := range []bool{false, true} {
		opt := setup(t)
		opt.SkipCaseRegisters = true
		file := "Другой файл.xlsx"
		if known {
			file = "Выгрузка.xlsx"
		}
		opt.FileMatching = map[string][]model.MatchPair{file: {{Source: "Должник", Export: "ФИО", IgnoreCase: true}, {Source: "Сумма долга", Export: "Переданная на принудительное исполнение сумма"}}}
		result, err := Run(context.Background(), opt, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if known {
			want = 3
		}
		if result.Found != want {
			t.Fatalf("known=%v found=%d want=%d", known, result.Found, want)
		}
	}
}

func TestConfiguredKeysRejectMissingExportColumn(t *testing.T) {
	opt := setup(t)
	opt.FileMatching = map[string][]model.MatchPair{"Выгрузка.xlsx": {{Source: "Должник", Export: "Отсутствующее поле"}}}
	_, err := Run(context.Background(), opt, nil)
	if err == nil || !strings.Contains(err.Error(), "Отсутствующее поле") {
		t.Fatal(err)
	}
}

func TestDifferentPoliciesMergeBirthConflicts(t *testing.T) {
	opt := setup(t)
	opt.SkipCaseRegisters = true
	fixture(t, filepath.Join(opt.Exports, "Ещё.xlsx"), exportFields, [][]any{{"Иванов Иван Иванович", "другой ИД", 100.25, "АО", "01.01.1981", "дело"}})
	opt.FileMatching = map[string][]model.MatchPair{"Ещё.xlsx": {{Source: "Должник", Export: "ФИО", IgnoreCase: true}, {Source: "Сумма долга", Export: "Переданная на принудительное исполнение сумма"}}}
	result, err := Run(context.Background(), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Две исходные строки конфликтуют по ДР из двух правил; строка с иным взыскателем
	// совпала только по новому правилу и получает единственную ДР.
	if result.Found != 1 {
		t.Fatalf("Конфликт разных правил не обработан: %+v", result)
	}
}
