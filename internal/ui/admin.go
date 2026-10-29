package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/browser"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *App) openAdmin() {
	if !platform.IsAdmin() || a.running {
		return
	}
	working := cloneSettings(a.cfg)
	catalog := settings.LoadCatalog(filepath.Join(a.local, "catalog.json"))
	var dlg *walk.Dialog
	var profile *walk.ComboBox
	var blocks *walk.ListBox
	var headless *walk.CheckBox
	var parallel *walk.NumberEdit
	var save, refresh *walk.PushButton
	var note, cacheNote *walk.Label
	busy := false
	index := func() int {
		i := profile.CurrentIndex()
		if i < 0 {
			return 0
		}
		return i
	}
	profileNames := []string{}
	for _, p := range working.Profiles {
		profileNames = append(profileNames, p.Name)
	}
	repaint := func() {
		if blocks == nil {
			return
		}
		names := []string{}
		for _, b := range working.Profiles[index()].Blocks {
			state := "выключен"
			if b.Enabled {
				state = "включён"
			}
			names = append(names, b.Name+" — "+state)
		}
		blocks.SetModel(names)
		if len(names) > 0 {
			blocks.SetCurrentIndex(0)
		}
		jobs, err := model.BuildJobs(working.Profiles[index()])
		if err != nil {
			note.SetText(err.Error())
		} else {
			note.SetText(fmt.Sprintf("Основной блок: 12 заданий для текущих трёх департаментов. Всего в регионе: %d заданий.", len(jobs)))
		}
	}
	edit := func(add bool) {
		if busy {
			return
		}
		p := &working.Profiles[index()]
		i := blocks.CurrentIndex()
		var b model.Block
		if add {
			b = model.Block{ID: fmt.Sprintf("block-%d", time.Now().UnixNano()), Name: fmt.Sprintf("Дополнительный блок %d", len(p.Blocks)+1), Enabled: true}
		} else {
			if i < 0 {
				return
			}
			b = p.Blocks[i]
		}
		if result, ok := editBlock(dlg, b, mergedFields(catalog, *p), *p); ok {
			if add {
				p.Blocks = append(p.Blocks, result)
			} else {
				p.Blocks[i] = result
			}
			repaint()
		}
	}
	def := Dialog{AssignTo: &dlg, Title: "Администрирование · " + platform.AccountLabel(), MinSize: Size{Width: 820, Height: 620}, Size: Size{Width: 860, Height: 660}, Layout: VBox{}, Children: []Widget{
		Label{Text: "Общие настройки выгрузки", Font: Font{Family: "Segoe UI", PointSize: 15, Bold: true}}, Label{Text: "Изменения применятся у всех перед следующей выгрузкой. Основной блок всегда включён."},
		ComboBox{AssignTo: &profile, Model: profileNames, OnCurrentIndexChanged: repaint},
		CheckBox{AssignTo: &headless, Text: "Скрывать браузер (headless)", Checked: working.Headless},
		Composite{Layout: HBox{}, Children: []Widget{Label{Text: "Одновременно активных заданий:"}, NumberEdit{AssignTo: &parallel, Value: float64(working.Parallel), MinValue: 1, MaxValue: 12, Decimals: 0}, HSpacer{}}},
		GroupBox{Title: "Основной блок — всегда включён", Layout: VBox{}, Children: []Widget{Label{Text: strings.Join(model.Statuses, "\n")}}},
		Label{AssignTo: &cacheNote, Text: "Проверка общего кэша полей…"},
		Label{Text: "Дополнительные блоки"}, ListBox{AssignTo: &blocks, OnItemActivated: func() { edit(false) }},
		Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "Добавить блок", OnClicked: func() { edit(true) }}, PushButton{Text: "Изменить блок", OnClicked: func() { edit(false) }}, PushButton{Text: "Удалить блок", OnClicked: func() {
			if busy {
				return
			}
			i := blocks.CurrentIndex()
			p := &working.Profiles[index()]
			if i >= 0 && walk.MsgBox(dlg, "Удаление блока", "Удалить выбранный дополнительный блок?", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
				p.Blocks = append(p.Blocks[:i], p.Blocks[i+1:]...)
				repaint()
			}
		}}, HSpacer{}, PushButton{AssignTo: &refresh, Text: "Обновить", OnClicked: func() {
			if busy {
				return
			}
			busy = true
			save.SetEnabled(false)
			refresh.SetEnabled(false)
			note.SetText("Получение полей и значений по департаментам…")
			profiles := append([]model.Profile(nil), working.Profiles...)
			hideBrowser := headless.Checked()
			ctx, cancel := context.WithCancel(context.Background())
			a.cancel = cancel
			a.running = true
			a.enable()
			go func() {
				updated := catalog
				engine, err := browser.New(ctx, a.local, "", hideBrowser, a.prompt)
				if err == nil {
					engine.SetSharedCatalog(settings.CatalogRoot(a.store.Shared))
					updated, err = engine.RefreshAll(ctx, profiles)
					engine.Close()
				}
				_, state := settings.LoadSharedCatalog(settings.CatalogRoot(a.store.Shared), a.local, profiles, time.Now())
				a.window.Synchronize(func() {
					catalog = updated
					cacheNote.SetText(state.Message())
					cancel()
					a.cancel = nil
					a.running = false
					busy = false
					save.SetEnabled(true)
					refresh.SetEnabled(true)
					repaint()
					if err != nil {
						walk.MsgBox(dlg, "Каталог обновлён частично", err.Error(), walk.MsgBoxIconWarning)
					} else {
						note.SetText("Поля и значения обновлены для всех настроенных баз и департаментов")
					}
				})
			}()
		}}}},
		Label{AssignTo: &note}, Label{Text: "Опубликованные настройки используются всеми пользователями приложения."},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &save, Text: "Сохранить для всех", OnClicked: func() {
			if busy {
				return
			}
			working.Headless = headless.Checked()
			working.Parallel = int(parallel.Value())
			if err := working.Validate(); err != nil {
				walk.MsgBox(dlg, "Проверьте настройки", err.Error(), walk.MsgBoxIconWarning)
				return
			}
			busy = true
			save.SetEnabled(false)
			refresh.SetEnabled(false)
			note.SetText("Сохранение общих настроек…")
			go func() {
				err := a.store.Publish(working, working.Revision)
				a.window.Synchronize(func() {
					busy = false
					save.SetEnabled(true)
					refresh.SetEnabled(true)
					if err != nil {
						walk.MsgBox(dlg, "Настройки не сохранены", err.Error(), walk.MsgBoxIconError)
						return
					}
					working.Revision++
					a.cfg = working
					dlg.Accept()
				})
			}()
		}}, PushButton{Text: "Закрыть", OnClicked: func() {
			if !busy {
				dlg.Cancel()
			}
		}}}},
	}}
	if err := def.Create(a.window); err != nil {
		a.error(err)
		return
	}
	profile.SetCurrentIndex(max(0, a.profiles.CurrentIndex()))
	a.owner = dlg
	defer func() { a.owner = a.window }()
	repaint()
	go func() {
		updated, state := settings.LoadSharedCatalog(settings.CatalogRoot(a.store.Shared), a.local, working.Profiles, time.Now())
		a.window.Synchronize(func() {
			if !dlg.IsDisposed() && !busy {
				catalog = updated
				cacheNote.SetText(state.Message())
			}
		})
	}()
	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if busy {
			*canceled = true
		}
	})
	dlg.Run()
	a.enable()
}

func mergedFields(catalog model.Catalog, p model.Profile) []model.Field {
	fields := map[string]model.Field{}
	for _, d := range p.Departments {
		for _, f := range catalog[model.Scope(d)] {
			if !f.Searchable {
				continue
			}
			old, ok := fields[f.Key]
			if !ok {
				old = f
				old.Options = nil
			}
			seen := map[string]bool{}
			for _, o := range old.Options {
				seen[o.Label] = true
			}
			for _, o := range f.Options {
				if !seen[o.Label] && o.Label != "Все" {
					old.Options = append(old.Options, model.Option{Label: o.Label, Value: o.Label})
					seen[o.Label] = true
				}
			}
			fields[f.Key] = old
		}
	}
	result := []model.Field{}
	for _, f := range fields {
		result = append(result, f)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}
func editBlock(owner walk.Form, b model.Block, fields []model.Field, p model.Profile) (model.Block, bool) {
	b.Rows = append([]model.Row(nil), b.Rows...)
	var dlg *walk.Dialog
	var name *walk.LineEdit
	var enabled, linked *walk.CheckBox
	var rows *walk.ListBox
	var note *walk.Label
	var ok, cancel *walk.PushButton
	repaint := func() {
		if rows == nil {
			return
		}
		names := []string{}
		for _, r := range b.Rows {
			label := r.Key
			for _, f := range fields {
				if f.Key == r.Key {
					label = f.Label
				}
			}
			names = append(names, label+": "+strings.Join(r.Values, "; "))
		}
		rows.SetModel(names)
		if len(names) > 0 {
			rows.SetCurrentIndex(0)
		}
		if linked != nil {
			b.Linked = linked.Checked()
		}
		test := p
		test.Blocks = []model.Block{b}
		jobs, err := model.BuildJobs(test)
		if err != nil {
			note.SetText(err.Error())
		} else {
			note.SetText(fmt.Sprintf("Предпросмотр: %d заданий в этом блоке. %s", len(jobs)-len(p.Departments)*len(model.Statuses), map[bool]string{true: "Каждая комбинация полей связана через И.", false: "Каждое значение выгружается отдельно."}[b.Linked]))
		}
	}
	edit := func(add bool) {
		i := rows.CurrentIndex()
		r := model.Row{}
		if !add {
			if i < 0 {
				return
			}
			r = b.Rows[i]
		}
		result, accept := editField(dlg, r, fields)
		if accept {
			for k, other := range b.Rows {
				if (add || k != i) && other.Key == result.Key {
					walk.MsgBox(dlg, "Поле уже добавлено", "Измените значения существующего поля", walk.MsgBoxIconWarning)
					return
				}
			}
			if add {
				b.Rows = append(b.Rows, result)
			} else {
				b.Rows[i] = result
			}
			repaint()
		}
	}
	definition := Dialog{AssignTo: &dlg, Title: "Блок выгрузки", MinSize: Size{Width: 740, Height: 480}, DefaultButton: &ok, CancelButton: &cancel, Layout: VBox{}, Children: []Widget{
		Label{Text: "Название блока"}, LineEdit{AssignTo: &name, Text: b.Name}, CheckBox{AssignTo: &enabled, Text: "Включить блок", Checked: b.Enabled}, CheckBox{AssignTo: &linked, Text: "Связывать поля (все комбинации значений)", Checked: b.Linked, OnCheckedChanged: repaint},
		ListBox{AssignTo: &rows, OnItemActivated: func() { edit(false) }}, Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "Добавить поле", OnClicked: func() { edit(true) }}, PushButton{Text: "Изменить поле", OnClicked: func() { edit(false) }}, PushButton{Text: "Удалить поле", OnClicked: func() {
			i := rows.CurrentIndex()
			if i >= 0 {
				b.Rows = append(b.Rows[:i], b.Rows[i+1:]...)
				repaint()
			}
		}}}}, Label{AssignTo: &note},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "Сохранить блок", OnClicked: func() {
			b.Name = strings.TrimSpace(name.Text())
			b.Enabled = enabled.Checked()
			b.Linked = linked.Checked()
			test := model.Settings{Schema: 1, Revision: 0, Headless: true, Parallel: 3, Profiles: []model.Profile{p}}
			test.Profiles[0].Blocks = []model.Block{b}
			if err := test.Validate(); err != nil {
				walk.MsgBox(dlg, "Проверьте блок", err.Error(), walk.MsgBoxIconWarning)
				return
			}
			dlg.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}
	if err := definition.Create(owner); err != nil {
		walk.MsgBox(owner, "Ошибка", err.Error(), walk.MsgBoxIconError)
		return b, false
	}
	repaint()
	code := dlg.Run()
	return b, code == walk.DlgCmdOK
}

func editField(owner walk.Form, row model.Row, fields []model.Field) (model.Row, bool) {
	if len(fields) == 0 {
		walk.MsgBox(owner, "Нет каталога", "Сначала обновите поля из Пирамиды", walk.MsgBoxIconInformation)
		return row, false
	}
	var dlg *walk.Dialog
	var search *walk.LineEdit
	var field *walk.ComboBox
	var choices *walk.ListBox
	var text *walk.TextEdit
	var hint *walk.Label
	var ok, cancel *walk.PushButton
	filtered := append([]model.Field(nil), fields...)
	original := row
	choose := func() {
		if field == nil || choices == nil || text == nil {
			return
		}
		i := field.CurrentIndex()
		if i < 0 || i >= len(filtered) {
			return
		}
		f := filtered[i]
		isSelect := f.Type == "select"
		choices.SetVisible(isSelect)
		text.SetVisible(!isSelect)
		if isSelect {
			names := []string{}
			selected := []int{}
			for _, o := range f.Options {
				names = append(names, o.Label)
				for _, v := range original.Values {
					if f.Key == original.Key && (v == o.Label || v == o.Value) {
						selected = append(selected, len(names)-1)
					}
				}
			}
			choices.SetModel(names)
			choices.SetSelectedIndexes(selected)
			hint.SetText("Выберите значения с Ctrl или Shift. Для каждого значения будет сформирована выгрузка.")
		} else {
			if f.Key == original.Key {
				text.SetText(strings.Join(original.Values, "\r\n"))
			} else {
				text.SetText("")
			}
			hint.SetText("Введите значения по одному в строке. Формат текста и дат соответствует фильтру Пирамиды.")
		}
	}
	rebuild := func() {
		if field == nil {
			return
		}
		query := ""
		if search != nil {
			query = strings.ToLower(search.Text())
		}
		filtered = nil
		names := []string{}
		selected := 0
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f.Label+" "+f.Key), query) {
				if f.Key == original.Key {
					selected = len(names)
				}
				filtered = append(filtered, f)
				names = append(names, f.Label)
			}
		}
		field.SetModel(names)
		if len(names) > 0 {
			field.SetCurrentIndex(selected)
		}
		choose()
	}
	def := Dialog{AssignTo: &dlg, Title: "Поле выгрузки", MinSize: Size{Width: 680, Height: 520}, Size: Size{Width: 760, Height: 600}, DefaultButton: &ok, CancelButton: &cancel, Layout: VBox{}, Children: []Widget{
		Label{Text: "Поиск поля"}, LineEdit{AssignTo: &search, OnTextChanged: rebuild}, ComboBox{AssignTo: &field, OnCurrentIndexChanged: choose}, Label{AssignTo: &hint}, ListBox{AssignTo: &choices, MultiSelection: true}, TextEdit{AssignTo: &text, Visible: false},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "Применить", OnClicked: func() {
			i := field.CurrentIndex()
			if i < 0 || i >= len(filtered) {
				return
			}
			f := filtered[i]
			values := []string{}
			if f.Type == "select" {
				for _, j := range choices.SelectedIndexes() {
					values = append(values, f.Options[j].Label)
				}
			} else {
				for _, line := range strings.Split(strings.ReplaceAll(text.Text(), "\r", ""), "\n") {
					if v := strings.TrimSpace(line); v != "" {
						values = append(values, v)
					}
				}
			}
			if len(values) == 0 {
				walk.MsgBox(dlg, "Нет значений", "Выберите или введите хотя бы одно значение", walk.MsgBoxIconWarning)
				return
			}
			row = model.Row{Key: f.Key, Values: values}
			dlg.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}
	if err := def.Create(owner); err != nil {
		walk.MsgBox(owner, "Ошибка", err.Error(), walk.MsgBoxIconError)
		return row, false
	}
	rebuild()
	code := dlg.Run()
	return row, code == walk.DlgCmdOK
}
