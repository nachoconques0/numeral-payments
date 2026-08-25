package response_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"numeral-payments/internal/bank"
	bankresponse "numeral-payments/internal/bank/response"
	"numeral-payments/internal/bank/xmlbank"
	paymentEntity "numeral-payments/internal/entity/payment"
)

const pollInterval = 20 * time.Millisecond

func TestPollerAppliesResponsesAndQuarantinesFiles(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		updaterErr error
		wantDir    string
		wantApply  []string
	}{
		{
			name:      "known payment",
			content:   "ID, STATUS\nJXJ984XXXZ, PROCESSED\n",
			wantDir:   bank.ProcessedDir,
			wantApply: []string{"JXJ984XXXZ:PROCESSED"},
		},
		{
			name:      "several rows",
			content:   "ID, STATUS\nAAAAAAAAAA, PROCESSED\nBBBBBBBBBB, REJECTED\n",
			wantDir:   bank.ProcessedDir,
			wantApply: []string{"AAAAAAAAAA:PROCESSED", "BBBBBBBBBB:REJECTED"},
		},
		{
			name:       "an unknown id keeps the file visible",
			content:    "ID, STATUS\nUNKNOWN123, PROCESSED\n",
			updaterErr: paymentEntity.ErrNotFound,
			wantDir:    bank.FailedDir,
			wantApply:  []string{"UNKNOWN123:PROCESSED"},
		},
		{
			name:       "an already applied response is not a failure",
			content:    "ID, STATUS\nJXJ984XXXZ, PROCESSED\n",
			updaterErr: paymentEntity.ErrAlreadyApplied,
			wantDir:    bank.ProcessedDir,
			wantApply:  []string{"JXJ984XXXZ:PROCESSED"},
		},
		{
			name:       "a contradicting response keeps the file visible",
			content:    "ID, STATUS\nJXJ984XXXZ, REJECTED\n",
			updaterErr: paymentEntity.ErrConflictingStatus,
			wantDir:    bank.FailedDir,
			wantApply:  []string{"JXJ984XXXZ:REJECTED"},
		},
		{
			name:    "malformed file is quarantined",
			content: "ID, STATUS\nA,B,C,D\n",
			wantDir: bank.FailedDir,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			folder := t.TempDir()
			if err := bank.EnsureFolders(folder); err != nil {
				t.Fatalf("prepare bank folder: %v", err)
			}
			if err := os.WriteFile(filepath.Join(folder, "response.csv"), []byte(test.content), 0o644); err != nil {
				t.Fatalf("write response: %v", err)
			}

			updater := &fakeUpdater{err: test.updaterErr}
			run(t, folder, updater)

			if _, err := os.Stat(filepath.Join(folder, test.wantDir, "response.csv")); err != nil {
				t.Fatalf("expected the file in %s/: %v", test.wantDir, err)
			}
			if _, err := os.Stat(filepath.Join(folder, "response.csv")); !os.IsNotExist(err) {
				t.Error("a consumed file must not stay in the bank folder")
			}
			if got := updater.applied(); !equal(got, test.wantApply) {
				t.Errorf("expected %v applied, got %v", test.wantApply, got)
			}
		})
	}
}

func TestPollerIgnoresFilesTheBankIsStillWriting(t *testing.T) {
	folder := t.TempDir()
	if err := bank.EnsureFolders(folder); err != nil {
		t.Fatalf("prepare bank folder: %v", err)
	}

	path := filepath.Join(folder, "response.csv")
	if err := os.WriteFile(path, []byte("ID, STATUS\nJXJ984XXXZ, PROCESSED\n"), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := os.Chtimes(path, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("set mtime: %v", err)
	}

	updater := &fakeUpdater{}
	run(t, folder, updater)

	if len(updater.applied()) != 0 {
		t.Error("a file still being written must not be read")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the file must be left in place for a later poll")
	}
}

// run polls the folder for a few intervals, then stops the poller the way the
// application does and waits for it to return.
func run(t *testing.T, folder string, updater *fakeUpdater) {
	t.Helper()

	poller := bankresponse.NewPoller(bankresponse.Options{
		Folder:   folder,
		Interval: pollInterval,
		Source:   xmlbank.NewAdapter(folder),
		Updater:  updater,
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		poller.Run(ctx)
	}()

	time.Sleep(6 * pollInterval)
	cancel()
	wg.Wait()
}

type fakeUpdater struct {
	mu   sync.Mutex
	seen []string
	err  error
}

func (u *fakeUpdater) ApplyBankResponse(_ context.Context, key string, status paymentEntity.Status) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.seen = append(u.seen, key+":"+string(status))
	return u.err
}

func (u *fakeUpdater) applied() []string {
	u.mu.Lock()
	defer u.mu.Unlock()

	return append([]string(nil), u.seen...)
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
