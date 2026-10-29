package ui

import (
	"fmt"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"strings"
)

const (
	resumeCancel  = 0
	resumeAll     = 1
	resumeMissing = 2
)

func chooseResume(owner walk.Form, found, total int, missing []model.Job) (int, error) {
	var dlg *walk.Dialog
	var missingButton, cancelButton *walk.PushButton
	choice := resumeCancel
	names := []string{}
	for _, j := range missing {
		names = append(names, j.Filename)
	}
	list := strings.Join(names, "\r\n")
	if list == "" {
		list = "Все ожидаемые файлы уже найдены."
	}
	_, err := (Dialog{AssignTo: &dlg, Title: "Выгрузка за сегодня", Icon: appIcon(), MinSize: Size{Width: 780, Height: 400}, Size: Size{Width: 860, Height: 460}, DefaultButton: &missingButton, CancelButton: &cancelButton, Layout: VBox{}, Children: []Widget{
		Label{Text: fmt.Sprintf("Найдено %d из %d ожидаемых файлов", found, total), Font: Font{Family: "Segoe UI", PointSize: 13, Bold: true}},
		Label{Text: "Недостающие или повреждённые файлы:"}, TextEdit{Text: list, ReadOnly: true},
		Label{Text: "Готовые файлы сохранятся до успешного получения новой выгрузки. Посторонние файлы не изменяются."},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{Text: "Перевыгрузить все", OnClicked: func() { choice = resumeAll; dlg.Accept() }}, PushButton{AssignTo: &missingButton, Text: "Довыгрузить недостающие", OnClicked: func() { choice = resumeMissing; dlg.Accept() }}, PushButton{AssignTo: &cancelButton, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}).Run(owner)
	return choice, err
}

func (a *App) abortStart(message string) {
	a.window.Synchronize(func() {
		a.running = false
		a.export.running = false
		a.cancel = nil
		a.enable()
		a.jobStatus.SetText(message)
		if a.closing {
			a.window.Close()
		}
	})
}
