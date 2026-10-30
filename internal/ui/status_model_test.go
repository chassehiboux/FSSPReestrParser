package ui

import (
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/pipeline"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"strings"
	"testing"
)

func TestDisabledStagesAreInvisible(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, analytics := range []bool{false, true} {
			for _, cases := range []bool{false, true} {
				m := &statusModel{mixed: mixed}
				names := registry.StageNames
				offset := 0
				if mixed {
					names = pipeline.StageNames
					offset = 1
					m.jobs = make([]model.Job, 12)
					m.stageNames = names
				}
				for i := range names {
					m.stages = append(m.stages, registry.Event{Stage: i, State: names[i]})
				}
				m.filterStages(registry.Options{SkipAnalytics: analytics, SkipCaseRegisters: cases}, offset)
				want := len(names)
				if mixed {
					want += 12
				}
				if analytics {
					want--
				}
				if cases {
					want -= 2
				}
				if m.RowCount() != want {
					t.Fatal("неверное число видимых этапов")
				}
				start := 0
				if mixed {
					start = 12
				}
				for row := start; row < m.RowCount(); row++ {
					title := m.Value(row, 1).(string)
					if analytics && strings.Contains(title, "аналитики") || cases && (strings.Contains(title, "по делу") || strings.Contains(title, "номеру дела")) {
						t.Fatal("виден отключённый этап", title)
					}
					if m.Value(row, 0) != row+1 || m.stageState(row) != title {
						t.Fatal("нарушено отображение индексов", row)
					}
				}
			}
		}
	}
	if strings.Contains(registry.BirthSummary(5, 0, true), "делу") {
		t.Fatal("отключённый поиск в итогах")
	}
	if !strings.Contains(registry.BirthSummary(5, 0, false), "делу") {
		t.Fatal("потерян включённый счётчик")
	}
}
