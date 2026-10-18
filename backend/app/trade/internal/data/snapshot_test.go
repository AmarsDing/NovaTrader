package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/accountsnapshot"
	entoutbox "server/ent/outbox"
	"server/ent/tradecash"
	"server/pkg/accountstate"
	"server/pkg/dbinit"
	"server/pkg/events"
	"server/pkg/migrate"
	"server/pkg/risk"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func accountTestClient(t *testing.T) *ent.Client {
	return accountTestClientDatabase(t, "novatrader_account_contract_test")
}

func accountTestClientDatabase(t *testing.T, database string) *ent.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// Deliberately distinct from the user's novatrader business ledger. Tests
	// never dispatch its outbox and cleanup only their own random book/IDs.
	port := int32(5432)
	if value := os.Getenv("NT_TEST_PG_PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatal("invalid NT_TEST_PG_PORT")
		}
		port = int32(parsed)
	}
	cfg := dbinit.Config{Host: "127.0.0.1", Port: port, User: "postgres", Password: "postgres", Database: database, SSLMode: "disable", Timeout: 3 * time.Second}
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		if os.Getenv("NT_TEST_PG_PORT") != "" {
			t.Fatal(err)
		}
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Schema(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestCompleteSnapshotAndOutboxCommitOrRollbackTogether(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "outbox-failure-rolls-back"}[fail], func(t *testing.T) {
			client := accountTestClient(t)
			ctx := context.Background()
			book := "ct_" + uuid.NewString()[:8]
			if fail {
				client.Outbox.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						return nil, errors.New("injected outbox failure")
					})
				})
			}
			now := time.Now()
			state := accountstate.Snapshot{Version: 1, Kind: "full", AccountID: "default", AccountType: risk.SIM, SnapshotID: uuid.NewString(), AsOf: now, TradingDay: now.In(tradecal.Shanghai()).Format("2006-01-02"), Cash: 100000, Equity: 100000, Available: 100000, DayStartKnown: true, DayStartEquity: 100000, Holdings: []risk.Holding{}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{}, Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: now}}
			cash := biz.Cash{Book: book, Cash: state.Cash, DayStartDate: state.TradingDay, DayStartEquity: state.DayStartEquity}
			repo := NewRepo(client, log.DefaultLogger)
			got, err := repo.CommitSnapshot(ctx, book, biz.AccountView{Book: book, Cash: state.Cash, Equity: state.Equity}, cash, state)
			if fail && err == nil {
				t.Fatal("injected outbox failure did not fail snapshot")
			}
			if !fail && err != nil {
				t.Fatal(err)
			}
			rows, err := client.AccountSnapshot.Query().Where(accountsnapshot.BookEQ(book)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cashRows, err := client.TradeCash.Query().Where(tradecash.BookEQ(book)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if len(rows) != 0 || len(cashRows) != 0 {
					t.Fatal("partial account commit survived rollback")
				}
				return
			}
			if len(rows) != 1 || len(cashRows) != 1 {
				t.Fatal("snapshot/baseline not committed")
			}
			stored, err := accountstate.Decode(rows[0].State)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			gotCanonical, err := accountstate.Decode(gotJSON)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Generation != int64(rows[0].ID) || !reflect.DeepEqual(stored, gotCanonical) {
				t.Fatal("stored full state/generation differs", stored, got)
			}
			boxes, err := client.Outbox.Query().Where(entoutbox.SubjectEQ(events.SubjectTradeAccount)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var eventIDs []string
			for _, box := range boxes {
				decoded, err := accountstate.Decode(box.Payload)
				if err == nil && decoded.SnapshotID == got.SnapshotID {
					eventIDs = append(eventIDs, box.EventID)
					if !reflect.DeepEqual(decoded, gotCanonical) {
						t.Fatal("outbox payload differs from stored state")
					}
				}
			}
			if len(eventIDs) != 1 {
				t.Fatal("missing/duplicate matching snapshot outbox", eventIDs)
			}
			t.Cleanup(func() {
				cleanup := context.Background()
				_, _ = client.Outbox.Delete().Where(entoutbox.EventIDIn(eventIDs...)).Exec(cleanup)
				_, _ = client.AccountSnapshot.Delete().Where(accountsnapshot.IDEQ(rows[0].ID)).Exec(cleanup)
				_, _ = client.TradeCash.Delete().Where(tradecash.IDEQ(cashRows[0].ID)).Exec(cleanup)
			})
		})
	}
}
