package queue

import (
	"archive/zip"
	"context"
	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fake struct {
	started     chan string
	release     chan struct{}
	mu          sync.Mutex
	active, max int
}

func (f *fake) Begin(ctx context.Context, j model.Job) (*Pending, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.max {
		f.max = f.active
	}
	f.mu.Unlock()
	f.started <- j.ID
	events := make(chan Event, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-f.release:
			events <- Event{State: Saved}
		}
	}()
	return &Pending{Events: events, Close: func() { f.mu.Lock(); f.active--; f.mu.Unlock() }}, nil
}
func TestQueueKeepsSlotsUntilSaveAndCancels(t *testing.T) {
	f := &fake{started: make(chan string, 10), release: make(chan struct{})}
	jobs := []model.Job{{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}, {ID: "5"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	terminal := map[string]string{}
	go func() {
		Run(ctx, jobs, 3, f, func(e Event) {
			if Terminal(e.State) {
				terminal[e.JobID] = e.State
			}
		})
		close(done)
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-f.started:
		case <-time.After(time.Second):
			t.Fatal("next export did not start while previous waits")
		}
	}
	select {
	case <-f.started:
		t.Fatal("exceeded active limit")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel stuck")
	}
	if len(terminal) != 5 || f.max != 3 {
		t.Fatalf("terminal=%v max=%d", terminal, f.max)
	}
}
func TestSaveRejectsHTMLAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad")
	os.WriteFile(bad, []byte("<html>Login</html>"), 0600)
	target := filepath.Join(dir, "result.xlsx")
	if SaveXLSX(bad, target) == nil {
		t.Fatal("HTML accepted")
	}
	good := filepath.Join(dir, "good")
	file, _ := os.Create(good)
	z := zip.NewWriter(file)
	for _, name := range []string{"[Content_Types].xml", "xl/workbook.xml"} {
		f, _ := z.Create(name)
		f.Write([]byte("<xml/>"))
	}
	z.Close()
	file.Close()
	if err := SaveXLSX(good, target); err != nil {
		t.Fatal(err)
	}
	if SaveXLSX(good, target) == nil {
		t.Fatal("overwrote existing file")
	}
	before, _ := os.ReadFile(target)
	if SaveResult(bad, target, true) == nil {
		t.Fatal("Invalid replacement accepted")
	}
	after, _ := os.ReadFile(target)
	if string(before) != string(after) {
		t.Fatal("Old result lost after failed replacement")
	}
	if err := SaveResult(good, target, true); err != nil {
		t.Fatal("Successful replacement failed:", err)
	}
	present, missing := Existing(dir, []model.Job{{Filename: "result.xlsx"}, {Filename: "absent.xlsx"}, {Filename: "bad"}})
	if len(present) != 1 || len(missing) != 2 {
		t.Fatalf("Resume classification: %d %d", len(present), len(missing))
	}
	if err := ClearEmptyResult(target, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("Unselected result removed")
	}
	if err := ClearEmptyResult(target, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("Stale rows left after confirmed empty result")
	}
}

type authRetry struct{ attempts int }

func (f *authRetry) Begin(context.Context, model.Job) (*Pending, error) {
	f.attempts++
	ch := make(chan Event, 1)
	if f.attempts == 1 {
		ch <- Event{State: Failed, RetryAuth: true}
	} else {
		ch <- Event{State: Saved}
	}
	return &Pending{Events: ch, Close: func() {}}, nil
}
func TestExpiredSessionRetriesOnlyCurrentJob(t *testing.T) {
	f := &authRetry{}
	states := []string{}
	Run(context.Background(), []model.Job{{ID: "one"}}, 1, f, func(e Event) { states = append(states, e.State) })
	if f.attempts != 2 || states[len(states)-1] != Saved {
		t.Fatalf("%d %v", f.attempts, states)
	}
	for _, s := range states {
		if s == Failed {
			t.Fatal("Transient auth expiry published as final failure")
		}
	}
}
