package ui

import (
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"strings"
)

func editElementKeys(owner walk.Form, b model.Block) (map[string][]model.MatchPair, bool) {
	rules := map[string][]model.MatchPair{}
	for k, v := range b.Matching {
		rules[k] = append([]model.MatchPair(nil), v...)
	}
	var dlg *walk.Dialog
	var list *walk.ListBox
	var ok, cancel *walk.PushButton
	var ids, labels []string
	for _, r := range b.Rows {
		for _, v := range r.Values {
			ids = append(ids, model.ElementID(r.Key, v))
			labels = append(labels, r.Key+": "+v)
		}
	}
	refresh := func() {
		names := make([]string, len(labels))
		for i, label := range labels {
			suffix := " — стандартный ключ"
			if pairs := rules[ids[i]]; len(pairs) > 0 {
				var parts []string
				for _, p := range pairs {
					parts = append(parts, p.Source+" → "+p.Export)
				}
				suffix = " — " + strings.Join(parts, "; ")
			}
			names[i] = label + suffix
		}
		selected := list.CurrentIndex()
		list.SetModel(names)
		if selected < 0 {
			selected = 0
		}
		if len(names) > 0 {
			list.SetCurrentIndex(selected)
		}
	}
	edit := func() {
		i := list.CurrentIndex()
		if i < 0 {
			return
		}
		pairs := rules[ids[i]]
		if len(pairs) == 0 {
			pairs = model.DefaultMatching()
		}
		if result, accepted := editMatchPairs(dlg, labels[i], pairs); accepted {
			rules[ids[i]] = result
			refresh()
		}
	}
	err := (Dialog{AssignTo: &dlg, Title: "Ключи элементов · " + b.Name, MinSize: Size{Width: 1000, Height: 440}, Layout: VBox{}, DefaultButton: &ok, CancelButton: &cancel, Children: []Widget{
		Label{Text: "Выберите элемент блока и настройте, какие поля должны совпасть одновременно."},
		ListBox{AssignTo: &list, OnItemActivated: edit},
		Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "Настроить ключи", OnClicked: edit}, PushButton{Text: "Вернуть стандартный ключ", OnClicked: func() {
			if i := list.CurrentIndex(); i >= 0 {
				delete(rules, ids[i])
				refresh()
			}
		}}}},
		Label{Text: "Для связанных условий используются все пары выбранных элементов. Изменения применятся после сохранения настроек админки."},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "Применить", OnClicked: func() { dlg.Accept() }}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}).Create(owner)
	if err != nil {
		walk.MsgBox(owner, "Ошибка", err.Error(), walk.MsgBoxIconError)
		return nil, false
	}
	refresh()
	return rules, dlg.Run() == walk.DlgCmdOK
}

func editMatchPairs(owner walk.Form, title string, pairs []model.MatchPair) ([]model.MatchPair, bool) {
	pairs = append([]model.MatchPair(nil), pairs...)
	var dlg *walk.Dialog
	var list *walk.ListBox
	var source, export *walk.LineEdit
	var fold *walk.CheckBox
	var ok, cancel *walk.PushButton
	refresh := func() {
		var names []string
		for _, p := range pairs {
			suffix := ""
			if p.IgnoreCase {
				suffix = " (без учёта регистра)"
			}
			names = append(names, p.Source+" → "+p.Export+suffix)
		}
		list.SetModel(names)
	}
	err := (Dialog{AssignTo: &dlg, Title: "Ключи · " + title, MinSize: Size{Width: 850, Height: 510}, Layout: VBox{}, DefaultButton: &ok, CancelButton: &cancel, Children: []Widget{
		Label{Text: "Все пары должны совпасть. Названия полей вводятся точно как заголовки в Excel."},
		ListBox{AssignTo: &list, OnCurrentIndexChanged: func() {
			i := list.CurrentIndex()
			if i >= 0 && i < len(pairs) {
				source.SetText(pairs[i].Source)
				export.SetText(pairs[i].Export)
				fold.SetChecked(pairs[i].IgnoreCase)
			}
		}},
		Label{Text: "Поле исходного файла"}, LineEdit{AssignTo: &source}, Label{Text: "Поле выгрузки ОВЗИД"}, LineEdit{AssignTo: &export}, CheckBox{AssignTo: &fold, Text: "Игнорировать регистр"},
		Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "Добавить пару", OnClicked: func() {
			pairs = append(pairs, model.MatchPair{Source: strings.TrimSpace(source.Text()), Export: strings.TrimSpace(export.Text()), IgnoreCase: fold.Checked()})
			refresh()
		}}, PushButton{Text: "Изменить выбранную пару", OnClicked: func() {
			i := list.CurrentIndex()
			if i >= 0 {
				pairs[i] = model.MatchPair{Source: strings.TrimSpace(source.Text()), Export: strings.TrimSpace(export.Text()), IgnoreCase: fold.Checked()}
				refresh()
			}
		}}, PushButton{Text: "Удалить пару", OnClicked: func() {
			if i := list.CurrentIndex(); i >= 0 {
				pairs = append(pairs[:i], pairs[i+1:]...)
				refresh()
			}
		}}}},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "Применить", OnClicked: func() {
			if err := model.ValidateMatching(pairs); err != nil {
				walk.MsgBox(dlg, "Проверьте ключи", err.Error(), walk.MsgBoxIconWarning)
				return
			}
			dlg.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}).Create(owner)
	if err != nil {
		walk.MsgBox(owner, "Ошибка", err.Error(), walk.MsgBoxIconError)
		return nil, false
	}
	refresh()
	return pairs, dlg.Run() == walk.DlgCmdOK
}
