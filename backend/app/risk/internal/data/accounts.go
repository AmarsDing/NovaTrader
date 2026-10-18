package data

import (
	"context"
	"fmt"

	"server/ent"
	"server/ent/accountsnapshot"
	"server/pkg/accountstate"
)

// LoadAccountSnapshots restores only complete, versioned states. Legacy summary
// rows are intentionally ignored, so absence continues to mean not ready.
func (r *Repo) LoadAccountSnapshots(ctx context.Context) ([]accountstate.Snapshot, error) {
	out := []accountstate.Snapshot{}
	for _, book := range []string{"paper", "live"} {
		row, err := r.client.AccountSnapshot.Query().Where(accountsnapshot.BookEQ(book), accountsnapshot.StateNotNil()).Order(ent.Desc(accountsnapshot.FieldID)).First(ctx)
		if ent.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		state, err := accountstate.Decode(row.State)
		if err != nil {
			return nil, fmt.Errorf("stored account %s/%d: %w", book, row.ID, err)
		}
		if (book == "paper" && state.AccountType != "SIM") || (book == "live" && state.AccountType != "LIVE") || state.Generation != int64(row.ID) {
			return nil, fmt.Errorf("stored account identity mismatch")
		}
		out = append(out, state)
	}
	return out, nil
}
