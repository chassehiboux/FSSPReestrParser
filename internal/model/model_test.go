package model

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func sample() Profile {
	return Profile{ID: "kgn", Name: "Курган", Bases: []Base{{ID: "base", URL: "https://example.test"}}, Departments: []Department{{BaseID: "base", ID: 39, Name: "Курган"}, {BaseID: "base", ID: 43, Name: "Курган-Восточный"}, {BaseID: "base", ID: 40, Name: "Шадринск"}}}
}

func TestParallelLimit(t *testing.T) {
	s := Settings{Schema: 1, Parallel: 20, Profiles: []Profile{sample()}}
	if err := s.Validate(); err != nil {
		t.Fatal("20 активных заданий должны быть разрешены", err)
	}
	for _, n := range []int{0, 21} {
		s.Parallel = n
		if s.Validate() == nil {
			t.Fatal("пропущен недопустимый лимит", n)
		}
	}
}
func TestPrimaryAndBlocks(t *testing.T) {
	p := sample()
	jobs, e := BuildJobs(p)
	if e != nil || len(jobs) != 12 {
		t.Fatalf("primary: %d %v", len(jobs), e)
	}
	for _, j := range jobs {
		if filepath.Base(j.Filename) != j.Filename {
			t.Fatal(j.Filename)
		}
	}
	p.Blocks = []Block{{ID: "extra", Name: "Блок", Enabled: true, Rows: []Row{{Key: "a", Values: []string{"A", "B"}}, {Key: "b", Values: []string{"1", "2", "3"}}}}}
	jobs, e = BuildJobs(p)
	if e != nil || len(jobs) != 27 {
		t.Fatalf("independent: %d %v", len(jobs), e)
	}
	p.Blocks[0].Linked = true
	jobs, e = BuildJobs(p)
	if e != nil || len(jobs) != 30 {
		t.Fatalf("linked: %d %v", len(jobs), e)
	}
}
func TestIdentityDedupAndCollisions(t *testing.T) {
	p := sample()
	p.Departments = p.Departments[:1]
	p.Blocks = []Block{{ID: "a", Name: "Блок", Enabled: true, Rows: []Row{{Key: "name", Values: []string{"x/y", "x:y", "x/y"}}}}}
	jobs, e := BuildJobs(p)
	if e != nil || len(jobs) != 6 {
		t.Fatalf("%d %v", len(jobs), e)
	}
	seen := map[string]bool{}
	for _, j := range jobs {
		if seen[j.Filename] {
			t.Fatal("collision", j.Filename)
		}
		seen[j.Filename] = true
	}
	p.Blocks = append(p.Blocks, Block{ID: "b", Name: "Копия", Enabled: true, Rows: p.Blocks[0].Rows})
	again, _ := BuildJobs(p)
	if len(again) != len(jobs) {
		t.Fatal("identical exports duplicated")
	}
	p.Blocks = p.Blocks[:1]
	p.Blocks[0].Rows[0].Values = []string{"x:y", "x/y"}
	reordered, _ := BuildJobs(p)
	names := map[string]string{}
	for _, j := range jobs {
		names[j.ID] = j.Filename
	}
	for _, j := range reordered {
		if names[j.ID] != j.Filename {
			t.Fatal("Filename changed after reorder", j.Filename)
		}
	}
}
func TestConcurrentRunsUseOneDayFolder(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	dirs := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := NewRunDir(root, time.Date(2026, 10, 29, 0, 0, 0, 0, time.Local))
			if e != nil {
				t.Error(e)
			}
			dirs <- p
		}()
	}
	wg.Wait()
	close(dirs)
	seen := map[string]bool{}
	for d := range dirs {
		seen[d] = true
		if _, e := os.Stat(d); e != nil {
			t.Fatal(e)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("Expected one day folder, got %d", len(seen))
	}
}
