package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestOldPlanIsNotResumedWithoutNewPrimaryStatus(t *testing.T) {
	req, local := request(t)
	r := Runner{Local: local}
	ctx := context.Background()
	old, _, err := r.open(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := digest(ctx, req.Registry.Source)
	if err != nil {
		old.close()
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]any{req, hash})
	sum := sha256.Sum256(raw)
	old.Signature = hex.EncodeToString(sum[:])
	old.Jobs = old.Jobs[:4]
	if err = old.save(); err != nil {
		old.close()
		t.Fatal(err)
	}
	previous := old.Dir
	old.close()
	fresh, resumed, err := r.open(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.close()
	if resumed || fresh.Dir == previous || len(fresh.Jobs) != 5 {
		t.Fatal("Продолжен устаревший план без пятого статуса", resumed, len(fresh.Jobs))
	}
}
