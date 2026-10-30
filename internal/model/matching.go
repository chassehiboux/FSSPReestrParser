package model

import "fmt"

func DefaultMatching() []MatchPair {
	return []MatchPair{{"Должник", "ФИО", true}, {"Номер ИД", "Номер ИД", false}, {"Сумма долга", "Переданная на принудительное исполнение сумма", false}, {"Взыскатель", "Взыскатель", false}}
}
func ElementID(field, value string) string { return field + "\x1f" + value }
func (p Profile) AllBlocks() []Block {
	b := Primary()
	b.Matching = p.PrimaryMatching
	return append([]Block{b}, p.Blocks...)
}
func ValidateMatching(pairs []MatchPair) error {
	if len(pairs) == 0 || len(pairs) > 32 {
		return fmt.Errorf("Выберите от 1 до 32 пар полей")
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		if p.Source == "" || p.Export == "" || seen[p.Source] {
			return fmt.Errorf("Поля ключа должны быть заполнены; поле исходника не должно повторяться")
		}
		seen[p.Source] = true
	}
	return nil
}
func MatchingFor(b Block, cs []Condition) []MatchPair {
	var result []MatchPair
	seen := map[MatchPair]bool{}
	for _, c := range cs {
		pairs := b.Matching[ElementID(c.Key, c.Value)]
		if len(pairs) == 0 {
			pairs = DefaultMatching()
		}
		for _, p := range pairs {
			if !seen[p] {
				seen[p] = true
				result = append(result, p)
			}
		}
	}
	return result
}
func FileMatching(p Profile) (map[string][]MatchPair, error) {
	jobs, err := BuildJobs(p)
	if err != nil {
		return nil, err
	}
	r := map[string][]MatchPair{}
	for _, j := range jobs {
		r[j.Filename] = j.Matching
	}
	return r, nil
}
