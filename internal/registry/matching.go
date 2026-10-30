package registry

import (
	"encoding/json"
	"fmt"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"strings"
	"time"
)

type matchPolicy struct {
	pairs  []model.MatchPair
	source []string
	valid  []bool
	index  map[string]*birthInfo
}
type configuredMatcher struct {
	files    map[string][]model.MatchPair
	policies map[string]*matchPolicy
}

func newConfiguredMatcher(files map[string][]model.MatchPair) *configuredMatcher {
	m := &configuredMatcher{files: files, policies: map[string]*matchPolicy{}}
	m.policy("")
	for file := range files {
		m.policy(file)
	}
	return m
}
func (m *configuredMatcher) policy(file string) *matchPolicy {
	pairs := m.files[file]
	if len(pairs) == 0 {
		pairs = model.DefaultMatching()
	}
	raw, _ := json.Marshal(pairs)
	id := string(raw)
	if p := m.policies[id]; p != nil {
		return p
	}
	p := &matchPolicy{pairs: pairs, index: map[string]*birthInfo{}}
	m.policies[id] = p
	return p
}
func (p *matchPolicy) exportNames() []string {
	var r []string
	for _, pair := range p.pairs {
		r = append(r, pair.Export)
	}
	return r
}
func (p *matchPolicy) key(row []value, cols map[string]int, source bool) (string, bool) {
	vals := make([]value, len(p.pairs))
	for i, pair := range p.pairs {
		name := pair.Export
		if source {
			name = pair.Source
		}
		col, exists := cols[name]
		if !exists {
			return "", false
		}
		v := at(row, col)
		if v.empty() {
			return "", false
		}
		if v.Kind == 'n' {
			v.Text = ""
		}
		if v.Kind == 's' && pair.IgnoreCase {
			v.Text = strings.ToLower(v.Text)
		}
		vals[i] = v
	}
	raw, _ := json.Marshal(vals)
	return string(raw), true
}
func (m *configuredMatcher) addSource(row []value, cols map[string]int) error {
	for _, p := range m.policies {
		for _, pair := range p.pairs {
			if _, ok := cols[pair.Source]; !ok {
				return fmt.Errorf("Нет поля исходника «%s», настроенного для сопоставления", pair.Source)
			}
		}
		k, ok := p.key(row, cols, true)
		p.source = append(p.source, k)
		p.valid = append(p.valid, ok)
		if ok && p.index[k] == nil {
			p.index[k] = &birthInfo{Dates: map[string]time.Time{}}
		}
	}
	return nil
}
func (p *matchPolicy) addExport(row []value, cols map[string]int, birth value, date1904 bool) {
	k, ok := p.key(row, cols, false)
	if ok && p.index[k] != nil {
		p.index[k].add(birth, date1904)
	}
}
func (m *configuredMatcher) resolve(row int) (*birthInfo, bool) {
	result := &birthInfo{Dates: map[string]time.Time{}}
	valid := false
	for _, p := range m.policies {
		valid = valid || p.valid[row]
		info := p.index[p.source[row]]
		if info == nil {
			continue
		}
		result.Matched = result.Matched || info.Matched
		result.Missing = result.Missing || info.Missing
		result.Invalid = result.Invalid || info.Invalid
		for k, v := range info.Dates {
			result.Dates[k] = v
		}
	}
	if !result.Matched {
		return nil, valid
	}
	return result, valid
}
