package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMatchingDefaultsOverridesAndUnion(t *testing.T) {
	b := Primary()
	c := Condition{Key: b.Rows[0].Key, Value: b.Rows[0].Values[0]}
	if !reflect.DeepEqual(MatchingFor(b, []Condition{c}), DefaultMatching()) {
		t.Fatal("Изменился прежний ключ")
	}
	custom := []MatchPair{{Source: "Номер дела", Export: "Номер дела"}}
	b.Matching = map[string][]MatchPair{ElementID(c.Key, c.Value): custom}
	if !reflect.DeepEqual(MatchingFor(b, []Condition{c}), custom) {
		t.Fatal("Не применено правило элемента")
	}
	union := MatchingFor(b, []Condition{c, {Key: "Другое", Value: "Значение"}})
	if len(union) != 5 {
		t.Fatal("Пары связанных элементов потеряны", union)
	}
	p := Profile{PrimaryMatching: b.Matching}
	raw, _ := json.Marshal(p)
	var loaded Profile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.AllBlocks()[0].Matching, b.Matching) {
		t.Fatal("Правила основного блока не сохранились")
	}
}
