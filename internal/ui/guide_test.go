package ui

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
)

func TestGuideCompletionAndEmbeddedPages(t *testing.T) {
	local := t.TempDir()
	if guideCompleted(local) {
		t.Fatal("Инструкция скрыта при первом запуске")
	}
	path := filepath.Join(local, "instruction.json")
	if err := settings.AtomicJSON(path, guideCompletion{Completed: true}); err != nil {
		t.Fatal(err)
	}
	if !guideCompleted(local) {
		t.Fatal("Завершённая инструкция не запомнена")
	}
	if err := os.WriteFile(path, []byte(`{"completed":`), 0600); err != nil {
		t.Fatal(err)
	}
	if guideCompleted(local) {
		t.Fatal("Повреждённая отметка скрыла инструкцию")
	}
	for i := 1; i <= guidePageCount; i++ {
		raw, err := guidePages.ReadFile(fmt.Sprintf("guide/page-%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil || img.Bounds().Dx() < 1500 || img.Bounds().Dy() < 1000 {
			t.Fatal("Нечитаемая страница инструкции", i, err)
		}
	}
}
